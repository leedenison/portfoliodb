package telemetry

import "context"

// CounterIncrementer increments a counter by name. Name is the key suffix only
// (e.g. "instrument.identify.attempts"); the implementation prepends the
// portfoliodb counters prefix.
type CounterIncrementer interface {
	Incr(ctx context.Context, name string)
}

// NoopCounter is a no-op implementation for tests and when telemetry is disabled.
type NoopCounter struct{}

func (NoopCounter) Incr(context.Context, string) {}
