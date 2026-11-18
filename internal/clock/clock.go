// Package clock lets code that uses TTLs take time from an interface, so tests
// can control it.
package clock

import (
	"sync"
	"time"
)

type Clock interface{ Now() time.Time }

// System is the real clock.
type System struct{}

func (System) Now() time.Time { return time.Now() }

// Fake is a manually advanced clock for tests. It is locked because the code
// under test reads it from its own goroutines while the test advances it.
type Fake struct {
	mu sync.Mutex
	T  time.Time
}

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.T
}

func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.T = f.T.Add(d)
}
