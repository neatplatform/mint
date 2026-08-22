[![Go Doc][godoc-image]][godoc-url]

# queue

`queue` provides in-memory queuing primitives for buffering and processing items concurrently.

It includes a bounded, lossy batching queue that groups items into batches and delegates them to a processing function,
either once a batch reaches its maximum size or once a timeout elapses, whichever happens first.

## Quick Start

### Batching

```go
package main

import (
  "fmt"
  "time"

  "github.com/neatplatform/mint/queue"
)

func main() {
  process := func(batch []string) {
    fmt.Println("processing batch:", batch)
  }

  q := queue.NewBatching(10, 100, time.Second, process)
  defer q.Stop()

  for i := 0; i < 25; i++ {
    q.Enqueue(fmt.Sprintf("item-%d", i))
  }
}
```

A `Batching` queue accumulates items and flushes them to a `process` function in batches,
either once `maxSize` items have been queued or once `timeout` has elapsed
since the first item of the current batch was queued, whichever happens first.

`Enqueue` is **best-effort** and **non-blocking**: once the queue reaches its `capacity`, new items are dropped.
Use `Flush` to trigger early processing of the current batch and `Stop` to gracefully drain and shut down the queue.


[godoc-url]: https://pkg.go.dev/github.com/neatplatform/mint/queue
[godoc-image]: https://pkg.go.dev/badge/github.com/neatplatform/mint/queue
