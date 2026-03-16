package client

import (
	"context"

	"golang.org/x/time/rate"
)

// RateLimiter enforces a shared call-rate limit across all EODHD API calls.
// A nil limiter (from perMin <= 0) imposes no limit.
type RateLimiter struct {
	lim *rate.Limiter
}

// NewRateLimiter creates a RateLimiter. perMin <= 0 means unlimited.
func NewRateLimiter(perMin int) *RateLimiter {
	if perMin <= 0 {
		return &RateLimiter{}
	}
	r := rate.Limit(float64(perMin) / 60.5)
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
