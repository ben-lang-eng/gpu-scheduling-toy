package pool_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ben-lang-eng/gpu-scheduling-toy/internal/pool"
)

// TestNewCreatesFullyFreePool checks that a freshly created pool reports every
// GPU as available and none in use.
func TestNewCreatesFullyFreePool(t *testing.T) {
	const size = 4

	p, err := pool.New(size)
	if err != nil {
		t.Fatalf("New(%d) returned unexpected error: %v", size, err)
	}

	stats := p.Stats()
	if stats.Capacity != size {
		t.Errorf("Capacity = %d, want %d", stats.Capacity, size)
	}
	if stats.Available != size {
		t.Errorf("Available = %d, want %d", stats.Available, size)
	}
	if stats.InUse != 0 {
		t.Errorf("InUse = %d, want 0", stats.InUse)
	}
}

// TestTryAcquireReservesUntilEmpty checks that TryAcquire hands out every GPU,
// then reports ErrNoCapacity, and that Release makes a GPU available again.
func TestTryAcquireReservesUntilEmpty(t *testing.T) {
	const size = 2

	p, err := pool.New(size)
	if err != nil {
		t.Fatalf("New(%d) returned unexpected error: %v", size, err)
	}

	// Reserving every GPU should succeed and leave the pool empty.
	first, err := p.TryAcquire()
	if err != nil {
		t.Fatalf("first TryAcquire returned unexpected error: %v", err)
	}
	if _, err := p.TryAcquire(); err != nil {
		t.Fatalf("second TryAcquire returned unexpected error: %v", err)
	}
	if got := p.Stats().InUse; got != size {
		t.Errorf("InUse = %d after reserving all GPUs, want %d", got, size)
	}

	// A further reservation must fail, because the pool is empty.
	if _, err := p.TryAcquire(); !errors.Is(err, pool.ErrNoCapacity) {
		t.Errorf("TryAcquire on empty pool returned %v, want ErrNoCapacity", err)
	}

	// Releasing one GPU must make exactly one available again.
	p.Release(first)
	if got := p.Stats().Available; got != 1 {
		t.Errorf("Available = %d after one Release, want 1", got)
	}
}

// TestAcquireTimesOutWhenPoolStaysEmpty checks that Acquire gives up with the
// context's deadline error when no GPU becomes free in time.
func TestAcquireTimesOutWhenPoolStaysEmpty(t *testing.T) {
	p, err := pool.New(1)
	if err != nil {
		t.Fatalf("New(1) returned unexpected error: %v", err)
	}

	// Reserve the only GPU so the pool is empty for the rest of the test.
	if _, err := p.TryAcquire(); err != nil {
		t.Fatalf("TryAcquire returned unexpected error: %v", err)
	}

	// Acquire should block, then fail once the short deadline passes.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, err := p.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Acquire returned %v, want context.DeadlineExceeded", err)
	}
}

// TestAcquireUnblocksOnRelease checks that a caller blocked in Acquire is
// served promptly once another caller releases a GPU.
func TestAcquireUnblocksOnRelease(t *testing.T) {
	p, err := pool.New(1)
	if err != nil {
		t.Fatalf("New(1) returned unexpected error: %v", err)
	}

	held, err := p.TryAcquire()
	if err != nil {
		t.Fatalf("TryAcquire returned unexpected error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Launch a background caller that waits for the single GPU to become free.
	// It reports the GPU it receives on the acquired channel.
	acquired := make(chan pool.GPU, 1)
	go func() {
		gpu, err := p.Acquire(ctx)
		if err != nil {
			t.Errorf("background Acquire returned unexpected error: %v", err)
			return
		}
		acquired <- gpu
	}()

	// While the GPU is still held, the waiter must remain blocked.
	select {
	case gpu := <-acquired:
		t.Fatalf("Acquire returned GPU %d before it was released", gpu)
	case <-time.After(50 * time.Millisecond):
		// Expected: still waiting.
	}

	// Releasing the GPU should unblock the waiter promptly.
	p.Release(held)

	select {
	case <-acquired:
		// Expected: the waiter was served.
	case <-time.After(time.Second):
		t.Fatal("Acquire did not return within one second of Release")
	}
}

// TestConcurrentAcquireReleaseNeverExceedsCapacity hammers the pool from many
// goroutines at once and checks two invariants: the number of GPUs held at any
// instant never exceeds the pool's capacity, and every GPU is returned once the
// workers finish. Run under the race detector (go test -race) it also proves
// the pool has no data races.
func TestConcurrentAcquireReleaseNeverExceedsCapacity(t *testing.T) {
	const (
		size    = 8
		workers = 100
		rounds  = 50
	)

	p, err := pool.New(size)
	if err != nil {
		t.Fatalf("New(%d) returned unexpected error: %v", size, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var live atomic.Int32 // number of GPUs held at this instant
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				gpu, err := p.Acquire(ctx)
				if err != nil {
					t.Errorf("Acquire returned unexpected error: %v", err)
					return
				}
				if held := live.Add(1); held > size {
					t.Errorf("%d GPUs held at once, exceeds capacity %d", held, size)
				}
				live.Add(-1)
				p.Release(gpu)
			}
		}()
	}

	wg.Wait()

	if got := p.Stats().Available; got != size {
		t.Errorf("Available = %d after all workers finished, want %d", got, size)
	}
}
