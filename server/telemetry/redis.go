package telemetry

import (
	"context"
	"strconv"
	"strings"

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

// CounterEntry is one counter name and its current value (for admin list).
type CounterEntry struct {
	Name  string
	Value int64
}

// ListCounters discovers all counter keys under the prefix and returns their
// suffix names and values. Used by the admin API.
func ListCounters(ctx context.Context, client *redis.Client, prefix string) ([]CounterEntry, error) {
	if client == nil {
		return nil, nil
	}
	if prefix == "" {
		prefix = defaultPrefix
	}
	var keys []string
	iter := client.Scan(ctx, 0, prefix+"*", 0).Iterator()
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, nil
	}
	vals, err := client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]CounterEntry, 0, len(keys))
	for i, k := range keys {
		name := strings.TrimPrefix(k, prefix)
		if name == "" {
			continue
		}
		var v int64
		if vals[i] != nil {
			if s, ok := vals[i].(string); ok && s != "" {
				v, _ = strconv.ParseInt(s, 10, 64)
			}
		}
		out = append(out, CounterEntry{Name: name, Value: v})
	}
	return out, nil
}
