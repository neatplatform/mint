# loki

A simple, best-effort client for pushing logs to a *Loki Push API* endpoint.

Each log line carries two distinct sets of key-value pairs:

  - **Labels** are indexed by Loki and they should be *low-cardinality*.
  - **Metadata** are not indexed and they can be *high-cardinality*.

## Behavior

  - Sends logs to a Loki Push API endpoint.
  - Batches and sends streams asynchronously.
  - Retries failed requests with exponential backoff.
  - Optimized for speed and bounded memory, keeping the logging hot path lean and minimal.
  - Best-effort: the caller is responsible for sending requests in the format expected by Loki.
  - Does not handle errors; only writes errors to standard error.
  - May silently drop a log in some cases (for example, when duplicate label names exist).

## Quick Start

```go
loki := loki.NewClient("my-tenant", "https://loki.example.com/loki/api/v1/push", loki.Options{
	TLSEnabled: true,
})
defer loki.Close()

loki.Send(
	"request executed",
	[]loki.Pair{
    {Name: "level", Value: "info"},
    {Name: "logger", Value: "my-app"},
  },
	[]loki.Pair{
    {Name: "requestID", Value: "07a5f191-3a5d-4b61-a67b-81c3152bcefe"},
  },
)
```

## Resources

  - [Loki HTTP API](https://grafana.com/docs/loki/latest/reference/loki-http-api/#ingest-logs)
  - [Send log data to Loki](https://grafana.com/docs/loki/latest/send-data)
