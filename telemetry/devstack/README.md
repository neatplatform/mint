# devstack

A local observability stack for developing and testing the `telemetry.Probe` package.

It runs a Loki-compatible log receiver, a Fluent Bit forwarder, and an OpenTelemetry Collector as containers,
each configured with a `debug`/`stdout` exporter instead of a real backend.
There is no persistent storage:
signals sent into the stack are printed to the container logs, not queryable or retained across restarts.
This is useful for confirming that logs, metrics, and traces are being produced and shipped during development.

## Prerequisites

- [Podman](https://podman.io)
- [Podman Compose](https://docs.podman.io/en/latest/markdown/podman-compose.1.html) plugin

## Quick Start

```bash
make up    # Start all containers in the background
make list  # List running containers and their status
make logs  # Tail logs from an interactively selected container
make down  # Stop and remove all containers
```

## Services

| **Container**           | **Host Port** | **Protocol** | **Endpoint**             | **Description**         |
|-------------------------|--------------:|--------------|--------------------------|-------------------------|
| Alloy                   | `3100`        | HTTP         | `POST /loki/api/v1/push` | Loki Push API           |
|                         | `3318`        | HTTP         | `/`                      | OTLP                    |
|                         | `3317`        | gRPC         |                          | OTLP                    |
| Fluent Bit              | `24224`       | TCP/UDP      |                          | Fluent Forward protocol |
|                         | `24318`       | HTTP/gRPC    | `/`                      | OTLP                    |
| OpenTelemetry Collector | `8006`        | TCP          |                          | Fluent Forward protocol |
|                         | `4318`        | HTTP         | `/`                      | OTLP                    |
|                         | `4317`        | gRPC         |                          | OTLP                    |
