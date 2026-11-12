package relay

import "sync/atomic"

// Inflight guards the configured maximum number of concurrent proxied
// requests. It never blocks or evicts an active stream.
type Inflight struct {
	current atomic.Int64
	max     int64
}

// NewInflight creates a guard with the given maximum. A non-positive maximum
// is treated as 64.
func NewInflight(max int) *Inflight {
	if max <= 0 {
		max = 64
	}
	return &Inflight{max: int64(max)}
}

// TryAcquire increments the counter when capacity remains.
func (f *Inflight) TryAcquire() bool {
	for {
		cur := f.current.Load()
		if cur >= f.max {
			return false
		}
		if f.current.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

// Release decrements the counter.
func (f *Inflight) Release() {
	if f.current.Add(-1) < 0 {
		f.current.Store(0)
	}
}

// Count returns the current number of in-flight requests.
func (f *Inflight) Count() int {
	n := f.current.Load()
	if n < 0 {
		return 0
	}
	return int(n)
}
