package client

import (
	"context"

	"golang.org/x/time/rate"
)

// RateLimiter enforces a shared call-rate limit across all Massive API categories.
// A nil limiter (from perMin <= 0) imposes no limit.
type RateLimiter struct {
	lim *rate.Limiter
}

// NewRateLimiter creates a RateLimiter. perMin <= 0 means unlimited.
func NewRateLimiter(perMin int) *RateLimiter {
	if perMin <= 0 {
		return &RateLimiter{}
	}
	// Use 60.5s instead of 60s so inter-call spacing is slightly longer
	// than the exact boundary (e.g. 5/min = one every 12.1s not 12.0s).
	r := rate.Limit(float64(perMin) / 60.5)
	// Burst of 1: calls are spaced evenly. A higher burst would allow
	// N calls immediately but then exceed the per-minute limit.
	return &RateLimiter{lim: rate.NewLimiter(r, 1)}
}

// Wait blocks until a token is available or ctx is cancelled.
// Returns nil immediately when no limit is configured.
func (rl *RateLimiter) Wait(ctx context.Context) error {
	if rl == nil || rl.lim == nil {
		return nil
	}
	return rl.lim.Wait(ctx)
}
