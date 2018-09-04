package parity

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

const WSReadColdDownTime = 5 * time.Second

type ParityRequestParams struct {
	Id      uint64        `json:"id"`
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type ParityResponseParams struct {
	Subscription string          `json:"subscription"`
	Result       json.RawMessage `json:"result"`
}

type ParityError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ParityResponseBody struct {
	Id     uint64               `json:"id"`
	Method string               `json:"method"`
	Result json.RawMessage      `json:"result"`
	Params ParityResponseParams `json:"params"`
	Error  *ParityError         `json:"error"`
}

type RPCResponseMap struct {
	sync.RWMutex
	m map[uint64]chan json.RawMessage
}

func (r *RPCResponseMap) Add(id uint64, ch chan json.RawMessage) {
	r.Lock()
	defer r.Unlock()
	r.m[id] = ch
}

func (r *RPCResponseMap) Delete(id uint64) {
	r.Lock()
	defer r.Unlock()
	delete(r.m, id)
}

func (r *RPCResponseMap) Get(id uint64) chan json.RawMessage {
	r.RLock()
	defer r.RUnlock()
	return r.m[id]
}

type SubscriptionMap struct {
	sync.RWMutex
	m map[string]chan json.RawMessage
}

func (s *SubscriptionMap) Add(id string, ch chan json.RawMessage) {
	s.Lock()
	defer s.Unlock()
	s.m[id] = ch
}

func (s *SubscriptionMap) Delete(id string) {
	s.Lock()
	defer s.Unlock()
	delete(s.m, id)
}

func (s *SubscriptionMap) Get(id string) (chan json.RawMessage, bool) {
	s.RLock()
	defer s.RUnlock()
	ch, ok := s.m[id]
	return ch, ok
}

type ParityWebsocketClient struct {
	sync.Mutex
	wsUri         string
	wsClient      *websocket.Conn
	log           *logrus.Entry
	rpcResponses  RPCResponseMap
	subscriptions SubscriptionMap
	connected     bool
}

func (pwc *ParityWebsocketClient) call(params ParityRequestParams) (json.RawMessage, error) {

	params.JSONRPC = "2.0"
	params.Id = rand.Uint64()

	// Set a lock here to prevent race condition. Thus, you can only have a call at a time.
	if !pwc.connected {
		return nil, fmt.Errorf("connection broken")
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	ch := make(chan json.RawMessage)
	pwc.rpcResponses.Add(params.Id, ch)
	defer pwc.rpcResponses.Delete(params.Id)

	pwc.Lock()
	err = pwc.wsClient.WriteMessage(websocket.TextMessage, b)
	if err != nil {
		return nil, err
	}
	pwc.Unlock()

	// TODO: add error handling when a socket is disconnected
	result := <-ch
	close(ch)

	return result, nil
}

func (pwc *ParityWebsocketClient) run() {
CONNECTION_LOOP:
	for {
		pwc.log.Info("Start establishing connection…")
		err := pwc.connect()
		if err != nil {
			pwc.log.WithError(err).WithField("wsUri", pwc.wsUri).Error("fail to establish websocket connection…")
			time.Sleep(WSReadColdDownTime)
			continue CONNECTION_LOOP
		}

		pwc.log.Info("Start reading messages…")
	MESSAGE_READ_LOOP:
		for {
			// Read websocket messages from websocket
			_, message, err := pwc.wsClient.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err) || websocket.IsCloseError(err) {
					pwc.log.WithError(err).Infoln("parity server connection is terminated")
					pwc.connected = false
					time.Sleep(WSReadColdDownTime)
					break MESSAGE_READ_LOOP
				} else {
					pwc.log.WithError(err).Infoln("can not read message")
				}
				continue MESSAGE_READ_LOOP
			}

			// Decode websocket messages
			var resp ParityResponseBody
			err = json.Unmarshal(message, &resp)
			if err != nil {
				pwc.log.WithError(err).WithField("message", message).Error("fail to parse parity response")
				continue MESSAGE_READ_LOOP
			}

			if resp.Result != nil {
				pwc.log.WithField("response", resp.Result).Debug("api call response")
				pwc.rpcResponses.Get(resp.Id) <- resp.Result
			} else if resp.Method == "parity_subscription" {
				subscriptionId := resp.Params.Subscription
				if ch, ok := pwc.subscriptions.Get(subscriptionId); ok {
					ch <- resp.Params.Result
				} else {
					pwc.log.WithField("subscriptionId", subscriptionId).Warn("subscription channel not found")
				}
			} else {
				pwc.log.WithField("response", string(message)).Warn("unexpected parity response")
			}
		}
	}
}

func (pwc *ParityWebsocketClient) GetBlockByNumber(block string, showTransaction bool) (json.RawMessage, error) {
	return pwc.call(ParityRequestParams{
		Method: "eth_getBlockByNumber",
		Params: []interface{}{
			block,
			showTransaction,
		},
	})
}

func (pwc *ParityWebsocketClient) Subscribe(subscriptionParams []interface{}) (<-chan json.RawMessage, error) {
	ch := make(chan json.RawMessage, 1000)

	params := ParityRequestParams{
		Method: "parity_subscribe",
		Params: subscriptionParams,
	}
	result, err := pwc.call(params)
	if err != nil {
		return nil, err
	}

	var subscriptionId string
	if err := json.Unmarshal(result, &subscriptionId); err != nil {
		return nil, err
	} else if subscriptionId == "" {
		return nil, fmt.Errorf("no subscription id found")
	}

	pwc.subscriptions.Add(subscriptionId, ch)
	return ch, nil
}

func (pwc *ParityWebsocketClient) connect() error {
	if !pwc.connected {
		c, _, err := websocket.DefaultDialer.Dial(pwc.wsUri, nil)
		if err != nil {
			return err
		}
		pwc.wsClient = c
		pwc.connected = true
	}
	return nil
}

func (pwc *ParityWebsocketClient) Run() {
	go pwc.run()
}

func NewParityWebsocketClient(wsUri string) *ParityWebsocketClient {
	log := logrus.New().WithField("module", "parity-websocket")

	return &ParityWebsocketClient{
		log:           log,
		wsUri:         wsUri,
		subscriptions: SubscriptionMap{sync.RWMutex{}, make(map[string]chan json.RawMessage)},
		rpcResponses:  RPCResponseMap{sync.RWMutex{}, make(map[uint64]chan json.RawMessage)},
	}
}
