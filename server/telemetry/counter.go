package telemetry

import "context"

// CounterIncrementer increments a counter by name. Name is the key suffix only
// (e.g. "instruments.resolution.totals.description.attempts"); the implementation prepends the
// portfoliodb counters prefix. IncrBy adds delta to the named counter (for running totals like token counts).
type CounterIncrementer interface {
	Incr(ctx context.Context, name string)
	IncrBy(ctx context.Context, name string, delta int64)
}

// NoopCounter is a no-op implementation for tests and when telemetry is disabled.
type NoopCounter struct{}

func (NoopCounter) Incr(context.Context, string) {}

func (NoopCounter) IncrBy(context.Context, string, int64) {}
