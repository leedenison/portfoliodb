package clock

import "time"

// Timer provides the current time. Production code passes nil (or a
// default Timer) which delegates to time.Now(). Tests supply a Timer
// whose NowFunc returns a fixed or advancing timestamp.
//
// A nil *Timer is safe to call and returns time.Now().
type Timer struct {
	NowFunc func() time.Time
}

// New returns a Timer that delegates to time.Now().
func New() *Timer { return &Timer{} }

// Now returns the current time. When NowFunc is nil (or the receiver
// is nil), it falls back to time.Now().
func (t *Timer) Now() time.Time {
	if t == nil || t.NowFunc == nil {
		return time.Now()
	}
	return t.NowFunc()
}
