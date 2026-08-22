package queue

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewBatching(t *testing.T) {
	t.Run("MaxSizeZero", func(t *testing.T) {
		assert.Panics(t, func() {
			NewBatching[string](0, 20, time.Second, nil)
		})
	})

	t.Run("CapacityLessThanMaxSize", func(t *testing.T) {
		assert.Panics(t, func() {
			NewBatching[string](10, 5, time.Second, nil)
		})
	})

	t.Run("TimeoutZero", func(t *testing.T) {
		assert.Panics(t, func() {
			NewBatching[string](10, 20, 0, nil)
		})
	})

	t.Run("ProcessFuncNil", func(t *testing.T) {
		assert.Panics(t, func() {
			NewBatching[string](10, 20, time.Second, nil)
		})
	})

	t.Run("OK", func(t *testing.T) {
		q := NewBatching(10, 20, time.Second, func([]string) {})

		assert.NotNil(t, q)
		assert.Equal(t, 10, q.maxSize)
		assert.Equal(t, 20, cap(q.queue))
		assert.Equal(t, time.Second, q.timeout)
		assert.NotNil(t, q.process)
		assert.NotNil(t, q.queue)
		assert.NotNil(t, q.flush)
		assert.NotNil(t, q.stop)
		assert.NotNil(t, q.done)
	})
}

func TestBatching(t *testing.T) {
	t.Run("MaxSizeReached", func(t *testing.T) {
		done := make(chan struct{}, 1)

		q := NewBatching(6, 12, time.Second, func(batch []string) {
			assert.Equal(t, []string{"A", "B", "C", "D", "E", "F"}, batch)
			done <- struct{}{}
		})

		defer q.Stop()

		assert.True(t, q.Enqueue("A"))
		assert.True(t, q.Enqueue("B"))
		assert.True(t, q.Enqueue("C"))
		assert.True(t, q.Enqueue("D"))
		assert.True(t, q.Enqueue("E"))
		assert.True(t, q.Enqueue("F"))

		assert.NotZero(t, q.Len())
		assert.Zero(t, q.Dropped())

		<-done
	})

	t.Run("TimeoutReached", func(t *testing.T) {
		done := make(chan struct{}, 1)

		q := NewBatching(6, 12, 50*time.Millisecond, func(batch []string) {
			assert.Equal(t, []string{"A", "B", "C"}, batch)
			done <- struct{}{}
		})

		defer q.Stop()

		assert.True(t, q.Enqueue("A"))
		assert.True(t, q.Enqueue("B"))
		assert.True(t, q.Enqueue("C"))

		assert.NotZero(t, q.Len())
		assert.Zero(t, q.Dropped())

		time.Sleep(100 * time.Millisecond)

		<-done
	})

	t.Run("ProcessPanics", func(t *testing.T) {
		calls := 0
		done := make(chan struct{}, 1)

		q := NewBatching(2, 4, time.Second, func(batch []string) {
			calls++
			if calls == 1 {
				panic("boom")
			}

			assert.Equal(t, []string{"C", "D"}, batch)
			done <- struct{}{}
		})

		defer q.Stop()

		// First batch will panic.
		assert.True(t, q.Enqueue("A"))
		assert.True(t, q.Enqueue("B"))

		// Second batch should be processed successfully.
		assert.True(t, q.Enqueue("C"))
		assert.True(t, q.Enqueue("D"))

		assert.NotZero(t, q.Len())
		assert.Zero(t, q.Dropped())

		<-done
	})

	t.Run("QueueFull", func(t *testing.T) {
		done := make(chan struct{}, 1)

		q := NewBatching(6, 6, time.Second, func(batch []string) {
			time.Sleep(50 * time.Millisecond)
			assert.Equal(t, []string{"A", "B", "C", "D", "E", "F"}, batch)
			done <- struct{}{}
		})

		defer q.Stop()

		assert.True(t, q.Enqueue("A"))
		assert.True(t, q.Enqueue("B"))
		assert.True(t, q.Enqueue("C"))
		assert.True(t, q.Enqueue("D"))
		assert.True(t, q.Enqueue("E"))
		assert.True(t, q.Enqueue("F"))

		// Will be dropped.
		assert.False(t, q.Enqueue("G"))
		assert.False(t, q.Enqueue("H"))

		assert.NotZero(t, q.Len())
		assert.NotZero(t, q.Dropped())

		<-done
	})

	t.Run("EnqueueAfterStop", func(t *testing.T) {
		done := make(chan struct{}, 1)

		q := NewBatching(6, 12, time.Second, func(batch []string) {
			assert.Equal(t, []string{"A", "B", "C", "D", "E", "F"}, batch)
			done <- struct{}{}
		})

		assert.True(t, q.Enqueue("A"))
		assert.True(t, q.Enqueue("B"))
		assert.True(t, q.Enqueue("C"))
		assert.True(t, q.Enqueue("D"))
		assert.True(t, q.Enqueue("E"))
		assert.True(t, q.Enqueue("F"))

		assert.NotZero(t, q.Len())
		assert.Zero(t, q.Dropped())

		q.Stop()

		// Will be dropped.
		assert.False(t, q.Enqueue("G"))
		assert.False(t, q.Enqueue("H"))

		assert.Zero(t, q.Len())
		assert.NotZero(t, q.Dropped())

		<-done
	})

	t.Run("Flush", func(t *testing.T) {
		done := make(chan struct{}, 1)

		q := NewBatching(6, 12, time.Second, func(batch []string) {
			assert.Equal(t, []string{"A", "B", "C", "D"}, batch)
			done <- struct{}{}
		})

		assert.True(t, q.Enqueue("A"))
		assert.True(t, q.Enqueue("B"))
		assert.True(t, q.Enqueue("C"))
		assert.True(t, q.Enqueue("D"))

		assert.NotZero(t, q.Len())
		assert.Zero(t, q.Dropped())

		// Wait for all enqueued items to be batched.
		time.Sleep(50 * time.Millisecond)

		q.Flush()
		q.Flush()
		q.Flush()

		<-done
	})

	t.Run("FlushAfterStop", func(t *testing.T) {
		done := make(chan struct{}, 1)

		q := NewBatching(6, 12, time.Second, func(batch []string) {
			assert.Equal(t, []string{"A", "B", "C", "D"}, batch)
			done <- struct{}{}
		})

		assert.True(t, q.Enqueue("A"))
		assert.True(t, q.Enqueue("B"))
		assert.True(t, q.Enqueue("C"))
		assert.True(t, q.Enqueue("D"))

		assert.NotZero(t, q.Len())
		assert.Zero(t, q.Dropped())

		// Wait for all enqueued items to be batched.
		time.Sleep(50 * time.Millisecond)

		q.Stop()
		q.Flush()
		q.Flush()

		<-done
	})

	t.Run("Stop", func(t *testing.T) {
		done := make(chan struct{}, 1)

		q := NewBatching(6, 12, time.Second, func(batch []string) {
			assert.Equal(t, []string{"A", "B", "C", "D"}, batch)
			done <- struct{}{}
		})

		assert.True(t, q.Enqueue("A"))
		assert.True(t, q.Enqueue("B"))
		assert.True(t, q.Enqueue("C"))
		assert.True(t, q.Enqueue("D"))

		assert.NotZero(t, q.Len())
		assert.Zero(t, q.Dropped())

		q.Stop()
		q.Stop()

		<-done
	})
}
