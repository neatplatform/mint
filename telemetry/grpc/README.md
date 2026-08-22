[![Go Doc][godoc-image]][godoc-url]

# gRPC Telemetry

This package can be used for building observable [gRPC](https://grpc.io) servers and clients.
It uses *interceptors* to intercept gRPC handlers and calls to report logs, metrics, and traces out-of-the-box.

## Quick Start

<details>
<summary>Demo</summary>

```go
package main

import (
  "context"
  "net"

  "google.golang.org/grpc"
  "google.golang.org/grpc/credentials/insecure"
  "google.golang.org/grpc/health"

  healthpb "google.golang.org/grpc/health/grpc_health_v1"

  "github.com/neatplatform/mint/telemetry"
  telegprc "github.com/neatplatform/mint/telemetry/grpc"
)

func main() {
  ctx := context.Background()

  probe := telemetry.NewProbe(
    telemetry.WithMetadata("my-service", "v0.1.0", nil),
    telemetry.WithStdoutLogger("info"),
    telemetry.WithOpenTelemetryMeter(),
  )
  defer probe.Close(ctx)

  // Server, instrumented with the server interceptors.
  si := telegprc.NewServerInterceptor(probe, telegprc.Options{})
  server := grpc.NewServer(
    grpc.UnaryInterceptor(si.Unary()),
    grpc.StreamInterceptor(si.Stream()),
  )

  healthServer := health.NewServer()
  healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
  healthpb.RegisterHealthServer(server, healthServer)

  lis, _ := net.Listen("tcp", "localhost:0")

  go func() {
    server.Serve(lis)
  }()
  defer server.Stop()

  // Client, instrumented with the client interceptors.
  ci := telegprc.NewClientInterceptor(probe, telegprc.Options{})
  conn, _ := grpc.NewClient(lis.Addr().String(),
    grpc.WithTransportCredentials(insecure.NewCredentials()),
    grpc.WithUnaryInterceptor(ci.Unary()),
    grpc.WithStreamInterceptor(ci.Stream()),
  )
  defer conn.Close()

  client := healthpb.NewHealthClient(conn)

  // Unary call, observed by the Unary interceptors on both sides.
  if resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{}); err == nil {
    probe.Logger().Infof("[unary] health check status: %v", resp.Status)
  }

  watchCtx, cancelWatch := context.WithCancel(context.Background())
  defer cancelWatch()

  // Server-streaming call, observed by the Stream interceptors on both sides.
  stream, _ := client.Watch(watchCtx, &healthpb.HealthCheckRequest{})

  if resp, err := stream.Recv(); err == nil {
    probe.Logger().Infof("[stream] health check status: %v", resp.Status)
  }
}
```
</details>

You can find production-grade examples [here](./examples).

## Behavior

For every unary or streaming call, both the interceptors:

  - Record **metrics** for request count, in-flight requests, duration, and message count/size.
    For streaming calls, sizes and counts are tallied per message as they are sent/received on the stream.
  - Emit a **log line** summarizing the call, at `info` for an `OK` status and `error` otherwise.
  - Start a **trace span**, propagating context via
    [OpenTelemetry propagation](https://opentelemetry.io/docs/specs/otel/context/api-propagators)
    so client and server spans are linked across the call.
  - Generate or reuse a **request UUID** and a **caller name**,
    propagated via gRPC metadata so a call can be correlated across logs.

The server interceptors additionally **recover panics** raised inside the wrapped handler,
turning them into a gRPC `Internal` error and an observed panic,
so a single failing call cannot take down the server.

Specific methods can be excluded from observability via `Options.ExcludeMethods`.

## Concepts

### Metadata

gRPC metadata is analogous to HTTP headers: a simple `key -> values` map attached to a call.

  - **Client → server**: the client adds metadata to the *outgoing* context (the context used to make the call).
    gRPC sends it to the server as HTTP/2 request headers.
  - **Server → client**: the server reads metadata from the *incoming* context (the context passed to the handler),
    and can send metadata back to the client via response headers (before the first message)
    or trailers (after the last message, alongside the final status).

Protocol-level response headers and trailers are standard framework metadata,
automatically injected by the gRPC runtime on the server and understood by the gRPC client.

Application-level response headers and trailers are custom metadata
that applications and interceptors attach for cross-cutting concerns.
Custom keys must be lowercase alphanumeric characters, hyphens, or underscores, and cannot start with `grpc-`.

This package uses metadata to propagate a request UUID and caller name across a call,
and to propagate OpenTelemetry trace context between client and server.

### Streaming

The interceptors in this package wrap every `SendMsg`/`RecvMsg` call on a stream to count and size messages for metrics.
Getting the stream lifecycle wrong is a common source of subtle bugs, so this section covers it in detail.

#### HTTP/2 Half-Close

A gRPC call is an HTTP/2 stream carrying two independent unidirectional flows of data: *client → server* and *server → client*.
HTTP/2 itself lets either side half-close its own flow independently,
by marking its last frame `END_STREAM`, while continuing to read from the other flow.

gRPC's Go API only exposes that symmetry on the client side:

  - The **client** can call `CloseSend()` to close its send direction while continuing to `Recv()` messages.
  - The **server** has no equivalent half-close: returning from the handler closes both directions at once and ends the RPC.

This asymmetry — the client can half-close and keep listening,
while the server can only close both directions at once — is the key to understand the different streaming flow below

#### gRPC Streaming Flows

**Server streaming** — one client request, many server responses:

```
                           Client                                Server
                             | ----------- Send(req) ------------> |  single request; handler starts
                             | ---------- CloseSend() -----------> |  client half-closed
                             |                                     |
                             | <--------- Send(resp 1) ----------- |
                             | <--------- Send(resp 2) ----------- |
                             | <--------- Send(resp 3) ----------- |
                             |                                     |  handler returns nil
                             | <--------- trailers (OK) ---------- |
           Recv() -> io.EOF  |                                     |  stream ended successfully
```

**Client streaming** — many client requests, one server response:

```
                           Client                                Server
                             | ---------- Send(req 1) -----------> |  handler loops: Recv()
                             | ---------- Send(req 2) -----------> |
                             | ---------- Send(req 3) -----------> |
                             | --------- CloseAndRecv() ---------> |  client half-closed
                             |                                     |  Recv() -> io.EOF
                             | <------ SendAndClose(resp) -------- |
                             |                                     |  handler returns nil
                             | <--------- trailers (OK) ---------- |
CloseAndRecv() -> resp, nil  |                                     |  stream ended successfully
```

**Bidirectional streaming** — either side sends any number of messages, in any order, until both are done:

```
                           Client                                Server
                             | ----------- Send(req 1) ----------> |  Recv() -> req 1
                             | <---------- Send(resp 1) ---------- |
                             | ----------- Send(req 2) ----------> |  Recv() -> req 2
                             | <---------- Send(resp 2) ---------- |
                             | ----------- CloseSend() ----------> |  client half-closed
                             |                                     |  Recv() -> io.EOF
                             | <---------- Send(resp 3) ---------- |  server can keep sending
                             | <---------- Send(resp 4) ---------- |
                             |                                     |  handler returns nil
                             | <---------- trailers (OK) --------- |
           Recv() -> io.EOF  |                                     |
```

A few related helper methods, and what they actually do:

  - **Client**
    - `CloseSend()`: half-closes the client's send direction.
      Always returns `nil` — the real outcome only becomes visible via `Recv()`.
    - `CloseAndRecv(m)`: `CloseSend()` followed by `RecvMsg(m)`
  - **Server**
    - `SendAndClose(m)`: alias for `SendMsg(m)`

#### Demystifying io.EOF

`io.EOF` implies different signals depending on which method, on which side, receives it:

| **Call**               | **Side** | **io.EOF** Semantic                                                              |
|------------------------|----------|----------------------------------------------------------------------------------|
| `Recv()` / `RecvMsg()` | Client   | The RPC completed **successfully** (`OK` status).                                |
| `Send()` / `SendMsg()` | Client   | The stream is broken — Call `Recv()` or `CloseAndRecv()` to get the real reason. |
| `Recv()` / `RecvMsg()` | Server   | The client closed its send direction by calling `CloseSend()`.                   |
| `Send()` / `SendMsg()` | Server   | The stream is broken — The real error is returned directly.                      |

#### Client Streaming Pitfalls

**The server can bail out early**

The server is not obliged to drain the client's stream.
It may call SendAndClose and return after a few messages —
early termination is legitimate (validation failed, quota exhausted, enough data to answer).

So the client's loop must treat `io.EOF` from Send as 
*"stop sending and go ask why"*, not as a failure in itself:

```go
for _, m := range messages {
  if err := stream.Send(m); err != nil {
    if err == io.EOF {
      break // stream's dead; CloseAndRecv has the reason
    }
    return err
  }
}

// May be (resp, nil) — a perfectly good outcome
resp, err := stream.CloseAndRecv()
```

**The handler that never sends**

If a client-streaming handler returns `nil` without ever calling `SendAndClose()`,
the client's `CloseAndRecv` returns `io.EOF` as its error.
There is no message to unmarshal into, yet the status was OK.
That combination reads like a transport failure and is a genuine source of bugs.

A client-streaming handler must send exactly one response on every success path.
If there iss nothing meaningful to return, send an empty message rather than nothing.

**Aborting mid-stream**

If the client wants to abandon the RPC early, its only tool is cancelling the context.
That sends `RST_STREAM`, and the server's `Recv` returns `codes.Canceled` — not `io.EOF`.
Handlers that only check for `io.EOF` will treat a cancellation as a hard error,
which is usually the right outcome but worth logging differently:

#### The Classic Deadlock

Both sides sitting in `Recv`, each waiting for the other to speak.
Nothing in gRPC detects this; the RPC simply hangs until the deadline.
This is the practical argument for always passing a context with deadline:

```go
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()
```


[godoc-url]: https://pkg.go.dev/github.com/neatplatform/mint/telemetry/grpc
[godoc-image]: https://pkg.go.dev/badge/github.com/neatplatform/mint/telemetry/grpc
