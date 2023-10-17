package in_mem_ds

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTSArrayQueue(t *testing.T) {

	t.Run("single goroutine", func(t *testing.T) {
		q := NewTSArrayQueue[int]()
		assert.Zero(t, q.Size())
		assert.True(t, q.Empty())
		assert.Equal(t, []int(nil), q.Values())

		q.Enqueue(3)
		assert.NotZero(t, q.Size())
		assert.False(t, q.Empty())
		assert.Equal(t, []int{3}, q.Values())

		elem, ok := q.Dequeue()
		if !assert.True(t, ok) {
			return
		}
		assert.Equal(t, 3, elem)
		assert.Zero(t, q.Size())
		assert.True(t, q.Empty())
		assert.Equal(t, []int{}, q.Values())
	})

	t.Run("several goroutines", func(t *testing.T) {

		t.Run("parallel Enqueue() calls followed by parallel Dequeue() calls (overlap) ", func(t *testing.T) {
			const goroutineCount = 10000
			q := NewTSArrayQueue[int]()

			wg := new(sync.WaitGroup)
			wg.Add(goroutineCount)

			for i := 0; i < goroutineCount; i++ {
				go func(i int) {
					defer wg.Done()

					if i < goroutineCount/2 {
						q.Enqueue(3)
					} else {
						q.Dequeue()
					}
				}(i)
			}

			wg.Wait()

			assert.Zero(t, q.Size())
			assert.True(t, q.Empty())
			assert.Equal(t, []int{}, q.Values())
		})

		t.Run("Enqueue() followed by parallel Enqueue() calls followed by parallel Dequeue() calls (overlap) ", func(t *testing.T) {
			q := NewTSArrayQueue[int]()

			wg := new(sync.WaitGroup)
			q.Enqueue(1)

			//parallel Enqueue() calls
			wg.Add(1000)

			for i := 0; i < 500; i++ {
				go func(i int) {
					defer wg.Done()
					q.Enqueue(3)
				}(i)
			}

			//parallel Dequeue() calls
			for i := 0; i < 500; i++ {
				go func(i int) {
					defer wg.Done()
					q.Dequeue()
				}(i)
			}

			wg.Wait()

			assert.Equal(t, 1, q.Size())
			assert.False(t, q.Empty())
			assert.Equal(t, []int{3}, q.Values())
		})

		t.Run("parallel Enqueue() and Dequeue() calls", func(t *testing.T) {
			const goroutineCount = 10000
			q := NewTSArrayQueue[int]()

			wg := new(sync.WaitGroup)
			wg.Add(goroutineCount)

			var enqueueCount atomic.Int32
			var successfulDequeueCount atomic.Int32

			for i := 0; i < goroutineCount; i++ {
				go func(i int) {
					defer wg.Done()

					if i%2 == 0 {
						q.Enqueue(3)
						enqueueCount.Add(1)
					} else {
						_, ok := q.Dequeue()
						if ok {
							successfulDequeueCount.Add(1)
						}
					}
				}(i)
			}

			wg.Wait()

			assert.Equal(t, int(enqueueCount.Load()-successfulDequeueCount.Load()), q.Size())
		})

	})
}