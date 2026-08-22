package main

import (
	"net/http"
	"sync"

	"github.com/neatplatform/mint/config"
	"github.com/neatplatform/mint/config/examples/log"
)

var params = struct {
	sync.Mutex
	LogLevel string
}{
	// Default values
	LogLevel: "info",
}

func main() {
	logger := new(log.Logger)
	logger.SetLevel(params.LogLevel)

	// Listening for any update to configs.
	ch := make(chan config.Update)
	go func() {
		for update := range ch {
			if update.Name == "LogLevel" {
				params.Lock()
				logger.SetLevel(params.LogLevel)
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

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		logger.Info("New request received!")
		w.WriteHeader(http.StatusOK)
	})

	logger.Info("Starting HTTP server on port 8080 ...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		panic(err)
	}
}
