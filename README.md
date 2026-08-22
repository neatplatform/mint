[![Go Doc][godoc-image]][godoc-url]
[![Build Status][workflow-image]][workflow-url]
[![Test Coverage][codecov-image]][codecov-url]

# Mint 🌱

Mint is a collection of small, **unopinionated** Go libraries — configuration loading,
graceful startup and shutdown, health checks, HTTP extensions, in-memory queuing,
and OpenTelemetry-based observability — for building **reliable**, **scalable**, and **observable** services.

## Why?

Every new Go service ends up rebuilding the same handful of things:

  - Reading config from flags, env vars, and files; wiring up health checks.
  - Coordinating startup and graceful shutdown.
  - Wrapping logs, metrics, and traces into something usable.

Rewriting these from scratch is time not spent on the actual product,
and each new implementation is a fresh chance to get it wrong.

Mint packages these concerns as small, focused, and independent libraries.
Each one solves a single problem and stays unopinionated about the rest of your stack,
so you can import only what you need without buying into a framework.

## Documentation

For complete documentation, please see [here](./docs/index.md)

## CI Checks

CI checks run on `pull_request` and `merge_queue` events, but only when the target branch is `main`.

**Why not on push to main?**

This repo uses merge queue, so all PRs land on `main` through the queue — running checks at that point would be redundant.

**Why not on other branches?**

Branches not targeting `main` skip CI checks entirely to reduce unnecessary runner usage.


[godoc-url]: https://pkg.go.dev/github.com/neatplatform/mint
[godoc-image]: https://pkg.go.dev/badge/github.com/neatplatform/mint
[workflow-url]: https://github.com/neatplatform/mint/actions/workflows/go.yml
[workflow-image]: https://github.com/neatplatform/mint/actions/workflows/go.yml/badge.svg
[codecov-url]: https://codecov.io/gh/neatplatform/mint
[codecov-image]: https://codecov.io/gh/neatplatform/mint/graph/badge.svg
