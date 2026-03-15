package client

import (
	"context"
	"testing"
	"time"
)

func TestRateLimiter_Wait_Unlimited(t *testing.T) {
	rl := NewRateLimiter(0) // zero = unlimited
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		if err := rl.Wait(ctx); err != nil {
			t.Fatalf("Wait returned error on unlimited limiter: %v", err)
		}
	}
}

func TestRateLimiter_Wait_NilSafe(t *testing.T) {
	var rl *RateLimiter
	if err := rl.Wait(context.Background()); err != nil {
		t.Fatalf("nil RateLimiter should be unlimited: %v", err)
	}
}

func TestRateLimiter_Wait_CancelledCtx(t *testing.T) {
	rl := NewRateLimiter(1) // 1 per minute = very slow
	ctx, cancel := context.WithCancel(context.Background())
	// Consume the single burst token.
	if err := rl.Wait(ctx); err != nil {
		t.Fatalf("first Wait should succeed: %v", err)
	}
	cancel()
	if err := rl.Wait(ctx); err == nil {
		t.Fatal("Wait should return error on cancelled ctx")
	}
}

func TestRateLimiter_Wait_Blocks(t *testing.T) {
	// 60 per minute = 1/sec, burst 1. First call consumes the token;
	// second call should block and hit the timeout.
	rl := NewRateLimiter(60)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := rl.Wait(ctx); err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if err := rl.Wait(ctx); err == nil {
		t.Fatal("second call should have blocked and timed out")
	}
}
