[![Go Doc][godoc-image]][godoc-url]

# HTTP Telemetry

This package can be used for building observable HTTP servers and clients.
It provides a *middleware* for wrapping http handlers and
a drop-in http client to report logs, metrics, and traces out-of-the-box.

## Quick Start

```go
package main

import (
  "context"
  "fmt"
  "net/http"

  "github.com/neatplatform/mint/telemetry"
  telehttp "github.com/neatplatform/mint/telemetry/http"
)

func main() {
  probe := telemetry.NewProbe(
    telemetry.WithMetadata("my-service", "v0.1.0", nil),
    telemetry.WithStdoutLogger("info"),
    telemetry.WithOpenTelemetryMeter(),
  )
  defer probe.Close(context.Background())

  // Wrap a handler to make it observable.
  m := telehttp.NewMiddleware(probe, telehttp.Options{})
  http.HandleFunc("/", m.Wrap(func(w http.ResponseWriter, r *http.Request) {
    fmt.Fprintln(w, "Hello, World!")
  }))

  // A drop-in, observable replacement for http.Client.
  client := telehttp.NewClient(http.DefaultClient, probe, telehttp.Options{})
  client.Get("/")

  http.ListenAndServe(":8080", nil)
}
```

You can find production-grade examples [here](./examples).

## Behavior

For every request, both the middleware and the client:

  - Record **metrics** for request count, in-flight requests, duration, and request/response body sizes.
  - Emit a **log line** summarizing the request, at a level chosen by the response status code (`info` / `warn` / `error`).
  - Start a **trace span**, propagating context via
    [OpenTelemetry propagation](https://opentelemetry.io/docs/specs/otel/context/api-propagators)
    so client and server spans are linked across the wire.
  - Generate or reuse a **request UUID** and a **caller name**, propagated via the
    `Request-UUID` and `Caller-Name` headers so a request can be correlated across logs.

The middleware additionally **recovers panics** raised inside the wrapped handler,
turning them into a `500` response and an observed panic,
so a single failing request cannot take down the server.

To keep metric cardinality low, path segments that look like UUIDs are replaced with `{uuid}`
before being recorded as the route label (customizable via `Options.UUIDRegexp`),
and specific routes can be excluded from observability altogether via `Options.ExcludeRoutes`.


[godoc-url]: https://pkg.go.dev/github.com/neatplatform/mint/telemetry/http
[godoc-image]: https://pkg.go.dev/badge/github.com/neatplatform/mint/telemetry/http
