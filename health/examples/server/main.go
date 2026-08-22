package main

import (
	"net/http"

	"github.com/neatplatform/mint/health"
)

func main() {
	logger := &Logger{}
	dbClient := NewClient("db-client")
	queueClient := NewClient("queue-client")

	health.SetLogger(logger)
	health.RegisterChecker(dbClient, queueClient)
	http.Handle("/health", health.HandlerFunc())

	logger.Infof("Listening on port 8080 ...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		panic(err)
	}
}
