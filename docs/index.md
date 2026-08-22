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

  - [`config`](../config) — config from flags, env vars, and files.
  - [`grace`](../grace) — graceful startup and shutdown.
  - [`health`](../health) — readiness and liveness checks.
  - [`httpx`](../httpx) — HTTP server/client extensions.
  - [`queue`](../queue) — in-memory queuing.
  - [`telemetry`](../telemetry) — OpenTelemetry logs, metrics, and traces.
  - [`factory`](../factory) — constructors for wiring the above together.
  - [`file`](../file) — file utilities.
  - [`ptr`](../ptr) — pointer helpers.

Each package has its own *README* with usage details and examples.
