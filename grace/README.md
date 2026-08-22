[![Go Doc][godoc-image]][godoc-url]

# grace

`grace` helps run Go services with **reliable startup** and **graceful shutdown**.
It coordinates client connections with retries, starts registered servers,
and handles signal-driven termination with configurable shutdown timeouts.

## Quick Start

```go
package main

import (
  "context"
  "errors"
  "fmt"
  "log"
  "net/http"
  "os"
  "time"

  "github.com/neatplatform/mint/grace"
)

// Logger is a simple logger implementing grace.Logger interface.
type Logger struct{}

func (l *Logger) Infof(format string, args ...interface{})  { log.Printf(format, args...) }
func (l *Logger) Errorf(format string, args ...interface{}) { log.Printf(format, args...) }

// Client is a mock client implementing grace.Client interface.
type Client struct {
  count int
  Name  string
}

func (c *Client) String() string {
  return c.Name
}

func (c *Client) Connect() error {
  c.count++
  time.Sleep(time.Second)

  // For testing retries
  if c.count < 3 {
    return errors.New("error on connecting client")
  }

  return nil
}

func (c *Client) Disconnect(context.Context) error {
  time.Sleep(time.Second)
  return nil
}

// Server is a simple http server implementing grace.Server interface.
type Server struct {
  name string
  *http.Server
}

func NewServer(name string, port uint16) *Server {
  mux := http.NewServeMux()
  mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
    _, _ = fmt.Fprintln(w, "Hello, World!")
  })

  server := &http.Server{
    Addr:    fmt.Sprintf(":%d", port),
    Handler: mux,
  }

  return &Server{
    name:   name,
    Server: server,
  }
}

func (s *Server) String() string {
  return s.name
}

func main() {
  l := &Logger{}
  c := &Client{Name: "db-client"}
  s := NewServer("api-server", 8080)

  grace.SetLogger(l)
  grace.RegisterClient(c)
  grace.RegisterServer(s)

  code := grace.StartAndWait()
  os.Exit(code)
}

```


[godoc-url]: https://pkg.go.dev/github.com/neatplatform/mint/grace
[godoc-image]: https://pkg.go.dev/badge/github.com/neatplatform/mint/grace
