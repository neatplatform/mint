[![Go Doc][godoc-image]][godoc-url]

# httpx

`httpx` provides extended functionality for HTTP applications beyond the standard `http` package.

It includes a retry helper for HTTP clients, a `Middleware` interface for chaining handlers, and
server/client error types for consistent error responses and structured client-side errors.

## Quick Start

### `Retry`

```go
package main

import (
  "log"
  "net/http"
  "time"

  "github.com/neatplatform/mint/httpx"
)

func main() {
  req, err := http.NewRequest(http.MethodGet, "https://httpbin.org/status/500", nil)
  if err != nil {
    log.Fatalln(err)
  }

  resp, err := httpx.Retry(http.DefaultClient, req, 3, 100*time.Millisecond, 2*time.Second)
  if err != nil {
    log.Fatalln(err)
  }

  defer resp.Body.Close()
}
```

### `ClientError`

```go
package main

import (
  "log"
  "net/http"

  "github.com/neatplatform/mint/httpx"
)

func call() error {
  resp, err := http.Get("https://httpbin.org/status/403")
  if err != nil {
    return err
  }

  defer resp.Body.Close()

  if resp.StatusCode != http.StatusOK {
    return httpx.NewClientError(resp)
  }

  return nil
}

func main() {
  if err := call(); err != nil {
    log.Fatalf("Error calling: %s", err)
  }

  log.Println("Succeeded!")
}
```

### `ServerError`

```go
package main

import (
  "errors"
  "fmt"
  "net/http"

  "github.com/neatplatform/mint/httpx"
)

type GreetRequest struct {
  Name string `json:"name"`
}

type GreetResponse struct {
  Message string `json:"message"`
}

func Greet(req *GreetRequest) (*GreetResponse, error) {
  if req.Name == "" {
    return nil, httpx.NewServerError(
      errors.New("name is required"),
      400,
    )
  }

  return &GreetResponse{
    Message: fmt.Sprintf("Hello, %s!\n", req.Name),
  }, nil
}

func main() {
  http.HandleFunc("/greet", func(w http.ResponseWriter, r *http.Request) {
    name := r.URL.Query().Get("name")

    resp, err := Greet(&GreetRequest{Name: name})
    if err != nil {
      httpx.Error(w, err)
      return
    }

    fmt.Fprintf(w, "%s", resp.Message)
  })

  fmt.Println("Listening on port 8080 ...")
  if err := http.ListenAndServe(":8080", nil); err != nil {
    panic(err)
  }
}
```

### `Middleware`

```go
package main

import (
  "fmt"
  "log"
  "net/http"
)

// AuthMiddleware is a middleware for authenticating requests.
type AuthMiddleware struct{}

func (m *AuthMiddleware) Wrap(next http.HandlerFunc) http.HandlerFunc {
  return func(w http.ResponseWriter, r *http.Request) {
    // Simulate authentication verification!
    if authH := r.Header.Get("Authorization"); authH != "Bearer token" {
      http.Error(w, "Unauthorized", http.StatusUnauthorized)
      return
    }

    next(w, r)
  }
}

// LoggerMiddleware is a middleware for logging requests.
type LoggerMiddleware struct{}

func (m *LoggerMiddleware) Wrap(next http.HandlerFunc) http.HandlerFunc {
  return func(w http.ResponseWriter, r *http.Request) {
    log.Printf("[Request] %s %s", r.Method, r.URL.Path)
    next(w, r)
  }
}

func main() {
  authM := &AuthMiddleware{}
  loggerM := &LoggerMiddleware{}

  // Chain the middlewares.
  http.Handle("/",
    authM.Wrap(
      loggerM.Wrap(
        func(w http.ResponseWriter, r *http.Request) {
          fmt.Fprintln(w, "Hello, World!")
        },
      ),
    ),
  )

  fmt.Println("Listening on port 8080 ...")
  if err := http.ListenAndServe(":8080", nil); err != nil {
    panic(err)
  }
}
```


[godoc-url]: https://pkg.go.dev/github.com/neatplatform/mint/httpx
[godoc-image]: https://pkg.go.dev/badge/github.com/neatplatform/mint/httpx
