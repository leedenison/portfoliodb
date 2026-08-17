package telemetry

import (
	"context"

	"github.com/redis/go-redis/v9"
)

const defaultPrefix = "portfoliodb:counters:"

// RedisCounter implements CounterIncrementer using Redis INCR.
type RedisCounter struct {
	client *redis.Client
	prefix string
}

// NewRedisCounter returns a counter that uses the given Redis client and key prefix.
// Prefix should be "portfoliodb:counters:" so keys are distinct from session keys.
func NewRedisCounter(client *redis.Client, prefix string) *RedisCounter {
	if prefix == "" {
		prefix = defaultPrefix
	}
	return &RedisCounter{client: client, prefix: prefix}
}

// Incr increments the counter for the given name (suffix). The full key is prefix + name.
func (r *RedisCounter) Incr(ctx context.Context, name string) {
	if r == nil || r.client == nil || name == "" {
		return
	}
	key := r.prefix + name
	_ = r.client.Incr(ctx, key).Err()
}

// IncrBy adds delta to the counter for the given name. Used for running totals (e.g. token usage).
func (r *RedisCounter) IncrBy(ctx context.Context, name string, delta int64) {
	if r == nil || r.client == nil || name == "" || delta == 0 {
		return
	}
	key := r.prefix + name
	_ = r.client.IncrBy(ctx, key, delta).Err()
}
