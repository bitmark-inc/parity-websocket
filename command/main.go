package main

import (
	"encoding/json"
	"fmt"
	"github.com/sirupsen/logrus"
	"sync"
	"time"

	"git.bitmark.com/system/parity-websocket"
)

func main() {

	c := parity.NewParityWebsocketClient("ws://localhost:8546")
	if err := c.Start(); err != nil {
		panic(err)
	}
	time.Sleep(time.Second)

	wg := sync.WaitGroup{}
	startTime := time.Now()
	logrus.Info("Start benchmarking 10000 blocks read…")
	for i := 3950000; i < 3960000; i++ {
		go func(blockNumber string) {
			wg.Add(1)
			// fmt.Println("blockNumber", blockNumber)
			b, err := c.GetBlockByNumber(blockNumber, false)
			if err != nil {
				panic(err)
			}
			data := map[string]interface{}{}
			json.Unmarshal(b, &data)
			// fmt.Println(blockNumber, "|", data["number"])
			if blockNumber != data["number"] {
				fmt.Println(blockNumber, "|", data["number"])
			}
			wg.Done()
		}(fmt.Sprintf("%#x", i))
	}
	wg.Wait()
	logrus.Infof("Finish benchmarking 10000 blocks read. %s used", time.Now().Sub(startTime).String())

	// subParams1 := []interface{}{
	// 	"eth_getBlockByNumber",
	// 	[]interface{}{"latest", false},
	// }
	subParams2 := []interface{}{
		"eth_getLogs",
		[]interface{}{
			map[string]interface{}{
				"topics":    []interface{}{"0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef", nil, nil, nil},
				"address":   "0xaaa157d2aaad0f9c43d5d757bef4ce5de650e7b9",
				"fromBlock": "0x0",
			},
		},
	}

	for {
		ch1, err := c.Subscribe(subParams2)
		if err != nil {
			logrus.WithError(err).Error("can not subscribe events")
			time.Sleep(5 * time.Second)
			continue
		}

		logrus.Info("Subscribe ethereum events…")
		for data := range ch1 {
			logrus.Info("Block Event:", string(data))
		}
		time.Sleep(5 * time.Second)
	}
}
