[![Go Doc][godoc-image]][godoc-url]

# telemetry

Build observable Go applications with a single, cohesive API that unifies the three pillars of
observability — logging, metrics, and tracing — into one **easy-to-use** and **hard-to-misuse** package.

It is built on top of [OpenTelemetry](https://opentelemetry.io), the industry standard for observability instrumentation.
OpenTelemetry does a commendable job consolidating the fragmented observability landscape under one umbrella,
but its broad scope and emphasis on backward compatibility make it complex to use correctly:

  - Configuring it requires wiring together numerous packages and options.
  - The API surface is large, and the specification itself is still evolving.
  - Each pillar is at a different maturity level, with breaking changes still ahead.

This package cuts through that complexity.
It provides a minimal, stable API that abstracts away the moving parts of OpenTelemetry,
so you can focus on instrumenting your application rather than configuring your observability stack.

The central concept is a **probe** — a single object that bundles a *logger*, a *meter*, and a *tracer*.
Initialise one probe, and your application is fully instrumented across all three observability signals from the start.

## Quick Start

```go
package main

import (
  "context"

  "github.com/neatplatform/mint/telemetry"
)

func main() {
  // Creating a new probe.
  p := telemetry.NewProbe(
    telemetry.WithMetadata("my-service", "v0.1.0", map[string]any{
      "environment": "test",
      "region":      "local",
    }),
    telemetry.WithStdoutLogger("info"),
    telemetry.WithOpenTelemetryMeter(),
  )

  defer p.Close(context.Background())

  // Setting the probe as the singleton instance.
  telemetry.SetProbe(p)

  p.Logger().Info("Hello, World!")
}
```

You can find more examples [here](./examples).

## Options

Most options can be set through environment variables.
This allows infrastructure teams to adjust observability pipeline configuration without code changes.
Options set explicitly in code take precedence over environment variables.

| Environment Variable | Description |
|----------------------|-------------|
| `PROBE_METADATA_NAME` | The name of the probe, included as an attribute on all telemetry it emits. |
| `PROBE_METADATA_VERSION` | The version of the probe, included as an attribute on all telemetry it emits. |
| `PROBE_METADATA_ATTRIBUTE_*` | Defines a custom metadata attribute for the probe. The suffix after the prefix becomes the lowercased attribute name. |
| `PROBE_LOGGER_STDOUT_ENABLED` | Enables the logger that writes logs to standard output. |
| `PROBE_LOGGER_STDOUT_LEVEL` | The minimum level logged by the stdout logger. |
| `PROBE_LOGGER_FILE_ENABLED` | Enables the logger that writes logs to a file. |
| `PROBE_LOGGER_FILE_LEVEL` | The minimum level logged by the file logger. |
| `PROBE_LOGGER_FILE_FILEPATH` | The path to the log file the file logger writes to. Defaults to `app.log` if not set. |
| `PROBE_LOGGER_FILE_MAXSIZE` | The maximum size of the log file in MB before it gets rotated. Defaults to `100` if not set. |
| `PROBE_LOGGER_FILE_MAXBACKUPS` | The maximum number of old, rotated log files to retain. Defaults to `5` if not set. |
| `PROBE_LOGGER_FILE_MAXAGE` | The maximum age in days of old, rotated log files to retain. Defaults to `7` if not set. |
| `PROBE_LOGGER_LOKI_ENABLED` | Enables the logger that sends logs to a Grafana Loki Push API endpoint. |
| `PROBE_LOGGER_LOKI_TENANT` | The Loki tenant (organization) ID to send logs under. |
| `PROBE_LOGGER_LOKI_LEVEL` | The minimum level logged by the Loki logger. |
| `PROBE_LOGGER_LOKI_LABELS` | A comma-separated list of **low-cardinality** keys to promote to indexed Loki labels; all other name-value pairs are sent as unindexed structured metadata. |
| `PROBE_LOGGER_LOKI_ENDPOINT` | The Loki Push API endpoint to send logs to. Defaults to `http://localhost:3100/loki/api/v1/push` if not set. |
| `PROBE_LOGGER_LOKI_TLS_ENABLED` | Enables TLS for the connection to the Loki endpoint. |
| `PROBE_LOGGER_FORWARD_ENABLED` | Enables the logger that sends logs to a Fluentd Forward protocol endpoint. |
| `PROBE_LOGGER_FORWARD_LEVEL` | The minimum level logged by the Forward logger. |
| `PROBE_LOGGER_FORWARD_TAG` | The tag included with logs sent over the Fluentd Forward protocol. |
| `PROBE_LOGGER_FORWARD_ENDPOINT` | The Fluentd Forward protocol endpoint to send logs to. Defaults to `localhost:24224` if not set. |
| `PROBE_LOGGER_FORWARD_TLS_ENABLED` | Enables TLS for the connection to the Forward endpoint. |
| `PROBE_LOGGER_OTEL_ENABLED` | Enables the logger that sends logs to an OpenTelemetry Collector endpoint. |
| `PROBE_LOGGER_OTEL_LEVEL` | The minimum level logged by the OpenTelemetry logger. |
| `PROBE_LOGGER_OTEL_HTTP_ENDPOINT` | The OTLP/HTTP endpoint the OpenTelemetry logger exports logs to. Defaults to `localhost:4318` if not set. |
| `PROBE_LOGGER_OTEL_GRPC_ENDPOINT` | The OTLP/gRPC endpoint the OpenTelemetry logger exports logs to. Defaults to `localhost:4317` if not set. |
| `PROBE_METER_OTEL_ENABLED` | Enables the OpenTelemetry meter. If neither an HTTP nor a gRPC endpoint is set, metrics are exposed via an HTTP handler in Prometheus format. |
| `PROBE_METER_OTEL_HTTP_ENDPOINT` | The OTLP/HTTP endpoint the OpenTelemetry meter exports metrics to. Defaults to `localhost:4318` if not set. |
| `PROBE_METER_OTEL_GRPC_ENDPOINT` | The OTLP/gRPC endpoint the OpenTelemetry meter exports metrics to. Defaults to `localhost:4317` if not set. |
| `PROBE_TRACER_OTEL_ENABLED` | Enables the OpenTelemetry tracer, which exports traces to an OpenTelemetry Collector endpoint. |
| `PROBE_TRACER_OTEL_HTTP_ENDPOINT` | The OTLP/HTTP endpoint the OpenTelemetry tracer exports traces to. Defaults to `localhost:4318` if not set. |
| `PROBE_TRACER_OTEL_GRPC_ENDPOINT` | The OTLP/gRPC endpoint the OpenTelemetry tracer exports traces to. Defaults to `localhost:4317` if not set. |

Every logger level above is one of `debug`, `info`, `warn`, `error`, or `none` (case-insensitive) and defaults to `info` if not set.
If any of the environment variables above is set to an invalid value, it is ignored.

## The Three Pillars of Observability

### Logging

Logs are discrete, timestamped records of events that occurred in your system.
They are the most flexible *signal* — a log entry can capture any arbitrary context, from a user action to an internal error.

Their strength is also their limitation. Because logs are unstructured and high-volume, they are expensive to store and query at scale.
Finding relevant information requires knowing what to search for in advance; logs are poor at surfacing unknown problems on their own.
They are also difficult to correlate across distributed services without additional tooling.

Logs are best used for **auditing**, capturing detailed context around **errors**, and post-incident **investigation**.

### Metrics

Metrics are numeric measurements collected at regular intervals and stored as **time-series** data.
Because they have low, fixed cardinality and are aggregated over time, they are compact and cheap to query — even at high throughput.

Metrics are the foundation of real-time monitoring.
They power **SLIs** (service-level indicators), **SLOs** (service-level objectives), dashboards, and automated alerts.
They are excellent at revealing **trends** and **distributions** across your entire traffic.

The trade-off is cardinality: metrics work well when the set of label values is bounded and known ahead of time.
Attaching high-cardinality labels (such as user IDs or request IDs) will cause storage and query costs to explode.

They are best used for **real-time monitoring**, **alerting**, **capacity planning**, and **SLO tracking**.

### Tracing

A trace records the end-to-end journey of a single request
as it propagates through your system — across service boundaries, queues, and databases.
Each step is captured as a **span**, and spans are linked together to form a complete picture of latency and causality.

Traces are invaluable for diagnosing performance bottlenecks and understanding how services interact.
However, because they capture rich, per-request data, they are expensive to collect in full.
Production systems typically sample a fraction of traces to manage cost,
which means traces describe individual requests but cannot be used to draw statistical conclusions about the overall system.

They are best used for **debugging** distributed systems, identifying latency **bottlenecks**, and understanding **request lifecycle**.

## Design Choices

This library makes deliberate, opinionated decisions to keep the API minimal and hard to misuse,
while delegating meaningful configuration choices to callers.
The most significant decisions are documented here for transparency and guiding future developments.

Whenever an interface unifies several underlying implementations,
it is tempting to let the quirks of one implementation leak through the abstraction.
Avoiding that means designing to the **lowest common denominator**:
the interface only exposes what every implementation can honor,
even if that means leaving out capabilities that some — but not all — implementations offer.

  - **A custom logger interface is provided.**
    - The OpenTelemetry Logger API operates at a low level, requiring callers to construct log records manually.
    - This library provides a higher-level interface to keep log entries consistent,
      while allowing integration with loggers not compatible with the OpenTelemetry Logging API.
  - **Simple logging API, efficient implementation.**
    - The logger accepts plain key-value pairs instead of strongly typed field builders.
      This keeps logging calls small and instrumentation from crowding business logic.
    - Implementations prioritise predictable performance: low allocation overhead, non-blocking I/O, and thread-safety.
      Log encoding uses static type switches instead of reflection.
    - Nested objects are intentionally not supported to keep encoding fast and downstream indexing simple.
      Slices of scalar types and `map[string]any` are encoded as JSON strings rather than actual arrays or objects.
      Everything else is stringified with `fmt.Sprintf("%+v", ...)`.
  - **Log levels are accepted as strings, returned as typed values.**
    - Applications commonly read log levels from environment variables or config files at runtime.
      Accepting a plain string avoids forcing callers to perform the conversion themselves.
    - A level is returned as a typed value, making any decision logic based on the current level straightforward.
  - **Two families of logging methods exist.**
    - The named-level methods (`Debug`, `Info`, `Warn`, `Error`) are used for
      logging a message accompanied by key-value pairs that carry contextual information.
    - The formatted variants (`Debugf`, `Infof`, `Warnf`, `Errorf`)
      follow the familiar Go convention for interpolated messages.
  - **Fatal log level not supported.**
    - A fatal log typically does two things: log an error message, then immediately call `os.Exit(1)`.
    - A logger should record information; it should never control the application lifecycle.
    - Because `os.Exit` abruptly terminates the process, it bypasses crucial cleanup operations, and code that calls it is hard to test.
    - A *fatal* log level has no place in a reusable package — the decision to `panic` or call `os.Exit` belongs to the user's code.
  - **Timestamps are formatted as RFC 3339 Nano.**
    - This provides sub-second precision while remaining a standard, human-readable, and widely parseable format.
  - **Multiple loggers can be used together.**
    - When more than one logger (*Stdout*, *File*, *Loki*, *Forward*, *OpenTelemetry*) is configured,
      each log entry is emitted to all of them.
    - Log aggregators are expected to consume logs from a single designated source;
      reading the same logs from multiple outputs risks duplicate ingestion.

## Best Practices

  - **Create one probe per process.**
    - Initialise a single probe per application
      and either pass it explicitly to the components that need it, or register it as a global instance.
    - Use the `.With` method to attach component-specific context to a logger,
      producing a derived logger that carries those attributes on every log entry.
      This way, multiple components can share the same underlying probe while each logs within its own context.
  - **Only configure the Stdout logger.**
    - It is recommended to enable only the *Stdout* logger and let a log collector/aggregator/shipper
      take care of collecting logs and shipping them for long-term storage and querying.
  - **Prefer the pull model for metrics.**
    - Consider configuring the *OpenTelemetry* meter without an OpenTelemetry Collector endpoint,
      and exposing metrics via an HTTP endpoint in Prometheus format instead of pushing them to a remote endpoint.
  - **Understand the pull vs. push model.**
    - The *Stdout* and *File* loggers, and the *Prometheus* meter, follow a pull model:
      they write data locally (logs to standard out or a file, metrics to an HTTP endpoint)
      and rely on an external service to collect it on its own schedule.
    - The *Loki* and *Forward* loggers, and the *OpenTelemetry* logger and meter, follow a push model:
      the application is responsible for actively sending data to a remote endpoint.
      All require configuring an endpoint, but give no special access to the application.
    - Neither model is strictly better — the right choice depends on your infrastructure.

## Resources

  - **Loki**
    - [Understand labels](https://grafana.com/docs/loki/latest/get-started/labels)
  - **Logging**
    - [Logs API](https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/logs/api.md)
    - [go.opentelemetry.io/otel/log](https://pkg.go.dev/go.opentelemetry.io/otel/log)
    - [go.opentelemetry.io/otel/sdk/log](https://pkg.go.dev/go.opentelemetry.io/otel/sdk/log)
  - **Metrics**
    - [Metrics API](https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/metrics/api.md)
    - [go.opentelemetry.io/otel/metric](https://pkg.go.dev/go.opentelemetry.io/otel/metric)
    - [go.opentelemetry.io/otel/sdk/metric](https://pkg.go.dev/go.opentelemetry.io/otel/sdk/metric)
  - **Tracing**
    - [Tracing API](https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/trace/api.md)
    - [go.opentelemetry.io/otel/trace](https://pkg.go.dev/go.opentelemetry.io/otel/trace)
    - [go.opentelemetry.io/otel/sdk/trace](https://pkg.go.dev/go.opentelemetry.io/otel/sdk/trace)
  - **OpenTelemetry**
    - [Collector Configuration](https://opentelemetry.io/docs/collector/configuration)
    - [Internal Architecture](https://github.com/open-telemetry/opentelemetry-collector/blob/main/docs/internal-architecture.md)
    - [Security](https://github.com/open-telemetry/opentelemetry-collector/blob/main/docs/security-best-practices.md)


[godoc-url]: https://pkg.go.dev/github.com/neatplatform/mint/telemetry
[godoc-image]: https://pkg.go.dev/badge/github.com/neatplatform/mint/telemetry
