package queue_test

import (
	"fmt"
	"time"

	"github.com/neatplatform/mint/queue"
)

func Example_batching() {
	done := make(chan []string, 10)

	// process is called by the queue's worker goroutine for each flushed batch.
	process := func(batch []string) {
		// Copy the batch: the underlying array is reused once process returns.
		done <- append([]string(nil), batch...)
	}

	// Create and configure a batching queue:
	// batches of up to 3 items, a queue capacity of 10 items, and a 200-millisecond flush timeout.
	q := queue.NewBatching(3, 10, 200*time.Millisecond, process)
	defer q.Stop()

	// Enqueuing a 3rd item reaches maxSize, so this batch is flushed automatically.
	q.Enqueue("A")
	q.Enqueue("B")
	q.Enqueue("C")
	fmt.Println(<-done)

	// Enqueuing 2 items leaves the batch pending until the timeout elapses,
	// at which point it is flushed automatically.
	q.Enqueue("D")
	q.Enqueue("E")
	fmt.Println(<-done)

	// Enqueuing 2 more items does not reach maxSize, so nothing is flushed yet.
	q.Enqueue("F")
	q.Enqueue("G")
	time.Sleep(50 * time.Millisecond) // Give the worker goroutine a moment to pick up the queued items.
	q.Flush()                         // Flush the current batch early instead of waiting for the timeout.
	fmt.Println(<-done)

	// Stop drains and flushes any remaining items before shutting down the queue.
	q.Enqueue("H")
	q.Stop()
	fmt.Println(<-done)
}
