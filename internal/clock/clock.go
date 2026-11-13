// Package clock lets code that uses TTLs take time from an interface, so tests
// can control it.
package clock

import "time"

type Clock interface{ Now() time.Time }

// System is the real clock.
type System struct{}

func (System) Now() time.Time { return time.Now() }

// Fake is a manually advanced clock for tests.
type Fake struct{ T time.Time }

func (f *Fake) Now() time.Time          { return f.T }
func (f *Fake) Advance(d time.Duration) { f.T = f.T.Add(d) }
