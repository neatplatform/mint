package main

import (
	"os"

	"github.com/neatplatform/mint/grace"
)

func main() {
	logger := &Logger{}
	dbClient := NewClient("db-client")
	queueClient := NewClient("queue-client")
	apiServer := NewServer("api-server", 8080)
	infoServer := NewServer("info-server", 8081)

	grace.SetLogger(logger)
	grace.RegisterClient(dbClient, queueClient)
	grace.RegisterServer(apiServer, infoServer)

	code := grace.StartAndWait()
	os.Exit(code)
}
