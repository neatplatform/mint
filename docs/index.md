# Mint

Mint is a collection of small, **unopinionated** Go libraries for building
**reliable**, **scalable**, and **observable** services:

  - Configuration loading
  - Graceful startup and shutdown
  - Health checks
  - HTTP extensions
  - In-memory queuing
  - OpenTelemetry-based observability.
  - More

## Philosophy

Every service ends up rebuilding the same scaffolding — config loading, graceful startup/shutdown,
health checks, logs/metrics/traces — and each fresh reimplementation is a new chance to get it wrong.

Mint factors this out into small, composable libraries instead of a framework: each package solves
one problem, has no opinion about the rest of your stack, and can be imported independently.

This takes the burden off individual developers and ensures every service gets the same
best-in-class, battle-tested foundation for these concerns — consistently, by default.

## Packages

  - [`config`](https://pkg.go.dev/github.com/neatplatform/mint/config) — config from flags, env vars, and files.
  - [`grace`](https://pkg.go.dev/github.com/neatplatform/mint/grace) — graceful startup and shutdown.
  - [`health`](https://pkg.go.dev/github.com/neatplatform/mint/health) — readiness and liveness checks.
  - [`httpx`](https://pkg.go.dev/github.com/neatplatform/mint/httpx) — HTTP server/client extensions.
  - [`queue`](https://pkg.go.dev/github.com/neatplatform/mint/queue) — in-memory queuing.
  - [`telemetry`](https://pkg.go.dev/github.com/neatplatform/mint/telemetry) — OpenTelemetry logs, metrics, and traces.
  - [`factory`](https://pkg.go.dev/github.com/neatplatform/mint/factory) — constructors for wiring the above together.
  - [`file`](https://pkg.go.dev/github.com/neatplatform/mint/file) — file utilities.
  - [`ptr`](https://pkg.go.dev/github.com/neatplatform/mint/ptr) — pointer helpers.

Each package has its own *README* with usage details and examples.
