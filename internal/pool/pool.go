// Package pool provides a concurrency-safe pool of interchangeable GPU
// resources. Callers acquire a GPU for exclusive use and release it when
// finished. When every GPU is in use, a caller may either fail fast
// (TryAcquire) or wait for one to become free (Acquire).
package pool

import (
	"context"
	"errors"
)

// ErrNoCapacity is returned when every GPU in the pool is currently in use.
var ErrNoCapacity = errors.New("pool: no GPU available")

// ErrInvalidSize is returned by New when the requested pool size is not
// positive.
var ErrInvalidSize = errors.New("pool: size must be positive")

// ErrNotReserved is returned by Release when the pool is already full, meaning
// the GPU was never reserved or has already been released.
var ErrNotReserved = errors.New("pool: release without an outstanding reservation")

// GPU identifies a single GPU within a pool. Identifiers are assigned
// sequentially from zero when the pool is created.
type GPU int

// Pool is a fixed-size set of interchangeable GPUs that can be shared safely
// across goroutines. The zero value is not usable; construct a Pool with New.
type Pool struct {
	// free carries the identifiers of every GPU not currently reserved.
	// Its buffer capacity equals the pool size, so returning a GPU never
	// blocks.
	free chan GPU
	size int
}

// New creates a Pool of size GPUs, all initially free. It returns
// ErrInvalidSize if size is not positive.
func New(size int) (*Pool, error) {
	if size <= 0 {
		return nil, ErrInvalidSize
	}
	free := make(chan GPU, size)
	for id := 0; id < size; id++ {
		free <- GPU(id)
	}
	return &Pool{free: free, size: size}, nil
}

// TryAcquire reserves a free GPU without blocking. It returns the reserved
// GPU, or ErrNoCapacity if every GPU is currently in use. A successful caller
// must return the GPU with Release once finished with it.
func (p *Pool) TryAcquire() (GPU, error) {
	select {
	case gpu := <-p.free:
		return gpu, nil
	default:
		return 0, ErrNoCapacity
	}
}

// Acquire reserves a free GPU, waiting until one becomes available or ctx is
// cancelled. It returns the reserved GPU, or the error from ctx.Err() if the
// context is cancelled first. A successful caller must return the GPU with
// Release once finished with it.
func (p *Pool) Acquire(ctx context.Context) (GPU, error) {
	select {
	case gpu := <-p.free:
		return gpu, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// Release returns a previously acquired GPU to the pool, making it available to
// other callers. It returns ErrNotReserved if the pool is already full, which
// means the GPU was never reserved or has already been released.
//
// Because the pool tracks only how many GPUs are free — not which specific ones
// are outstanding — releasing an in-range identifier that was never acquired
// can still succeed while the pool is not full. Callers are trusted to release
// only GPUs they hold; the HTTP layer validates the identifier range, and this
// limitation is documented in the README.
func (p *Pool) Release(gpu GPU) error {
	select {
	case p.free <- gpu:
		return nil
	default:
		return ErrNotReserved
	}
}

// Stats is a point-in-time snapshot of pool utilisation. The JSON tags define
// the field names used when the snapshot is sent over the HTTP API.
type Stats struct {
	Capacity  int `json:"capacity"`
	Available int `json:"available"`
	InUse     int `json:"inUse"`
}

// Capacity returns the total number of GPUs in the pool.
func (p *Pool) Capacity() int {
	return p.size
}

// Stats returns a snapshot of how many GPUs are free and in use. Because other
// goroutines may acquire or release GPUs concurrently, the values can be stale
// the moment they are returned; they are intended for monitoring, not for
// gating allocation decisions.
func (p *Pool) Stats() Stats {
	available := len(p.free)
	return Stats{
		Capacity:  p.size,
		Available: available,
		InUse:     p.size - available,
	}
}