package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/neatplatform/mint/config"
	"github.com/neatplatform/mint/config/examples/log"
)

var params = struct {
	sync.Mutex
	LogLevel      string
	ServerAddress string
}{
	// Default values
	LogLevel:      "info",
	ServerAddress: "http://localhost:8080",
}

func main() {
	logger := new(log.Logger)
	logger.SetLevel(params.LogLevel)

	// Server address
	endpoint := "/"
	url := fmt.Sprintf("%s%s", params.ServerAddress, endpoint)

	// Listening for any update to configs.
	ch := make(chan config.Update)
	go func() {
		for update := range ch {
			switch update.Name {
			case "LogLevel":
				params.Lock()
				logger.SetLevel(params.LogLevel)
				params.Unlock()
			case "ServerAddress":
				params.Lock()
				url = fmt.Sprintf("%s%s", params.ServerAddress, endpoint)
				params.Unlock()
			}
		}
	}()

	// Watching for config changes.
	close, err := config.Watch(&params, []chan config.Update{ch})
	if err != nil {
		panic(err)
	}

	defer close()

	// Sending requests to server.
	logger.Info("Start sending requests ...")

	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{},
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			logger.Error(err)
			continue
		}

		resp, err := client.Do(req)
		if err != nil {
			logger.Error(err)
			continue
		}

		logger.Infof("Response received: status_code: %d", resp.StatusCode)
	}
}
