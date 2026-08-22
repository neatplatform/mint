package main

import (
	"fmt"
	"net/http"

	"github.com/neatplatform/mint/config"
	"github.com/neatplatform/mint/config/examples/log"
)

var params = struct {
	Environment string
	LogLevel    string
	AuthToken   string
}{
	// Default values
	Environment: "local",
	LogLevel:    "debug",
}

func main() {
	err := config.Pick(&params, config.Telepresence())
	if err != nil {
		panic(err)
	}

	logger := new(log.Logger)
	logger.SetLevel(params.LogLevel)

	logger.Debugf("[%s] Auth Token: %s", params.Environment, params.AuthToken)

	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		logger.Infof("[%s] New request received!", params.Environment)
		_, _ = fmt.Fprintln(w, "Hello, World!")
	})

	logger.Infof("[%s] Starting HTTP server on port 8080 ...", params.Environment)
	if err := http.ListenAndServe(":8080", nil); err != nil {
		panic(err)
	}
}
