package queue

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// ProcessFunc processes one batch of items.
//
// The slice is only valid for the duration of the call:
// the underlying array is reused for subsequent batches once process returns,
// so implementations must not retain or use it afterwards.
type ProcessFunc[T any] func([]T)

// Batching accumulates items and delegates them to a ProcessFunc in batches,
// either once maxSize items have been queued or once timeout has elapsed
// since the first item of the current batch was queued, whichever happens first.
//
// Batching is a bounded, lossy batching queue.
//
// Backpressure is applied via a fixed-size buffered channel:
// when the queue is full, Enqueue drops new items immediately.
//
// Items are processed sequentially in batches by a single worker goroutine.
type Batching[T any] struct {
	mu       sync.Mutex
	stopped  bool
	stopOnce sync.Once
	dropped  atomic.Int64

	maxSize int
	timeout time.Duration
	process ProcessFunc[T]

	queue chan T
	flush chan struct{}
	stop  chan struct{}
	done  chan struct{}
}

// newBatching creates a new batching and starts its background processing goroutine.
//
// maxSize is the batch size that triggers immediate processing.
//
// capacity is the maximum number of queued items waiting to be processed.
// Once the queue is full, Enqueue drops additional items.
//
// timeout is the maximum duration between the first item entering a batch
// and that batch being processed, even if maxSize has not been reached.
//
// process is called from the worker goroutine to handle each batch as it is flushed.
// If process panics, the panic is recovered, logged to stderr, and the batch is discarded;
// the worker goroutine continues running and the queue keeps processing subsequent batches.
func NewBatching[T any](maxSize, capacity int, timeout time.Duration, process ProcessFunc[T]) *Batching[T] {
	if maxSize <= 0 {
		panic("maxSize must be greater than zero")
	}

	if capacity < maxSize {
		panic("capacity must be greater than or equal to maxSize")
	}

	if timeout <= 0 {
		panic("timeout must be greater than zero")
	}

	if process == nil {
		panic("process function cannot be nil")
	}

	q := &Batching[T]{
		maxSize: maxSize,
		timeout: timeout,
		process: process,

		queue: make(chan T, capacity), // Bound the channel size
		flush: make(chan struct{}, 1), // Flush is asynchronous (fire-and-forget)
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}

	go q.run()

	return q
}

// Enqueue attempts to add an item to the queue.
// It is best-effort and non-blocking.
//
// It returns false if the queue is currently full or if the queue has been marked as stopped.
func (q *Batching[T]) Enqueue(item T) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Fail-fast path: queue stopped, drop item
	if q.stopped {
		q.dropped.Add(1)
		return false
	}

	select {
	case q.queue <- item:
		// Item accepted
		return true
	default:
		// Queue is full, drop item
		q.dropped.Add(1)
		return false
	}
}

// Len returns the number of items currently waiting in the queue.
//
// This excludes items already pulled into a batch and being processed by the worker goroutine.
// It is just a snapshot: the value may be stale by the time the caller acts on it.
func (q *Batching[T]) Len() int {
	// len on a channel is safe to call concurrently.
	return len(q.queue)
}

// Dropped returns the cumulative number of items that Enqueue has rejected
// since the queue was created, whether because the queue was full or already stopped.
func (q *Batching[T]) Dropped() int {
	return int(q.dropped.Load())
}

// Flush requests an early processing of the current batch,
// without waiting for maxSize or timeout conditions.
//
// Flush is asynchronous and returns immediately. It does not block.
//
// If multiple Flush calls occur while a previous flush request is still pending,
// they are coalesced into a single pending flush request.
//
// Flush has no effect if the queue has already been stopped.
func (q *Batching[T]) Flush() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.stopped {
		return
	}

	select {
	case q.flush <- struct{}{}:
	default:
		// Flush already pending
	}
}

// Stop stops the queue and waits for all queued items to be processed.
// It blocks until the background processing goroutine has exited.
//
// Stop is idempotent and safe for concurrent use.
func (q *Batching[T]) Stop() {
	q.stopOnce.Do(func() {
		q.mu.Lock()
		q.stopped = true
		q.mu.Unlock()

		close(q.stop) // Signal the runner to stop accepting new items.
		<-q.done      // Wait for the runner to finish processing all items.
	})
}

// run consumes queued items, grouping them into batches, until Stop is called.
// It owns the only timer used for flush deadlines and the only slice used to accumulate a batch:
// both are allocated once and reused for the lifetime of the queue.
func (q *Batching[T]) run() {
	defer close(q.done)

	// Batch is exclusively owned by the worker goroutine.
	batch := make([]T, 0, q.maxSize)

	// Allocate the timer once, upfront, and keep it idle until the first item of a batch arrives.
	// Reusing this single timer for the lifetime of the queue avoids the repeated cost of allocation and garbage-collection.
	timer := time.NewTimer(q.timeout)
	defer timer.Stop()

	// stopTimer cancels an active timer.
	// It must be called before the timer is reset, and whenever a batch is flushed before the timer fires,
	// so the timer is left idle and its channel empty until the next batch starts.
	stopTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}

	stopTimer()

	// flush processes the current batch, if non-empty,
	// stops any active timeout, and resets the batch for reuse.
	//
	// A panic from process is recovered so a single bad batch cannot
	// crash the worker goroutine and take down the whole queue.
	flush := func() {
		stopTimer()

		if len(batch) == 0 {
			return
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintln(os.Stderr, "queue: process panicked:", r)
				}
			}()
			q.process(batch)
		}()

		batch = batch[:0] // Reuse the slice
	}

	for {
		select {
		case item := <-q.queue:
			batch = append(batch, item)

			// Start the timeout window when the first item of a new batch arrives.
			if len(batch) == 1 {
				timer.Reset(q.timeout)
			}

			// Encode the invariant more defensively.
			if len(batch) >= q.maxSize {
				flush()
			}

		case <-timer.C:
			flush()

		case <-q.flush:
			flush()

		case <-q.stop:
			// Drain remaining queued items and flush final batch before exit.
			for {
				select {
				case item := <-q.queue:
					batch = append(batch, item)
				default:
					flush()
					return
				}
			}
		}
	}
}
