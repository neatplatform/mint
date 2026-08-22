package main

import (
	"flag"
	"fmt"
	"net/url"
	"time"

	"github.com/neatplatform/mint/config"
)

var params = struct {
	Port      uint16
	LogLevel  string
	Timeout   time.Duration
	Endpoints []url.URL
}{
	Port:     8080,            // default port
	LogLevel: "info",          // default log level
	Timeout:  2 * time.Minute, // default API call timeout
}

func main() {
	if err := config.Pick(&params); err != nil {
		panic(err)
	}

	flag.Parse()

	fmt.Printf("\nParams: %+v\n\n", params)
}
