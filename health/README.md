[![Go Doc][godoc-image]][godoc-url]

# health

`health` provides HTTP health check handlers for Go services.
It runs registered checkers concurrently with a configurable timeout
and returns standard readiness-style status codes.

## Quick Start

```go
package main

import (
  "context"
  "log"
  "net/http"
  "time"

  "github.com/neatplatform/mint/health"
)

type Logger struct{}

func (l *Logger) Errorf(format string, args ...interface{}) {
  log.Printf(format, args...)
}

type Client struct {
  Name string
}

func (c *Client) String() string {
  return c.Name
}

func (c *Client) HealthCheck(context.Context) error {
  // Simulate health checking!
  time.Sleep(100 * time.Millisecond)
  return nil
}

func main() {
  l := &Logger{}
  c := &Client{Name: "db-client"}

  health.SetLogger(l)
  health.RegisterChecker(c)
  http.Handle("/health", health.HandlerFunc())

  log.Println("Listening on port 8080 ...")
  if err := http.ListenAndServe(":8080", nil); err != nil {
    panic(err)
  }
}
```


[godoc-url]: https://pkg.go.dev/github.com/neatplatform/mint/health
[godoc-image]: https://pkg.go.dev/badge/github.com/neatplatform/mint/health
