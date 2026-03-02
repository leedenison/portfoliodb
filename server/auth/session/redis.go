package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// RedisStore implements Store using Redis.
type RedisStore struct {
	client *redis.Client
	prefix string
	// Sliding extension: on Get we extend TTL by this much (e.g. 72h).
	extendTTL time.Duration
}

// NewRedisStore returns a Redis-backed session store.
func NewRedisStore(client *redis.Client, keyPrefix string, extendTTL time.Duration) *RedisStore {
	if keyPrefix == "" {
		keyPrefix = "portfoliodb:session:"
	}
	return &RedisStore{client: client, prefix: keyPrefix, extendTTL: extendTTL}
}

func (r *RedisStore) key(sessionID string) string {
	return r.prefix + sessionID
}

// Create implements Store.
func (r *RedisStore) Create(ctx context.Context, data *Data, maxAge time.Duration) (string, error) {
	sessionID := uuid.New().String()
	data.CreatedAt = time.Now()
	data.ExpiresAt = data.CreatedAt.Add(maxAge)
	data.LastSeenAt = data.CreatedAt
	payload, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	key := r.key(sessionID)
	if err := r.client.Set(ctx, key, payload, maxAge).Err(); err != nil {
		return "", fmt.Errorf("redis set: %w", err)
	}
	return sessionID, nil
}

// Get implements Store. If slidingWindow > 0, extends TTL by slidingWindow when the session is found.
func (r *RedisStore) Get(ctx context.Context, sessionID string, slidingWindow time.Duration) (*Data, error) {
	key := r.key(sessionID)
	val, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("redis get: %w", err)
	}
	var data Data
	if err := json.Unmarshal(val, &data); err != nil {
		return nil, err
	}
	if time.Now().After(data.ExpiresAt) {
		_ = r.client.Del(ctx, key).Err()
		return nil, nil
	}
	// Sliding: extend TTL
	if slidingWindow > 0 {
		data.LastSeenAt = time.Now()
		newExpiry := data.LastSeenAt.Add(slidingWindow)
		if newExpiry.After(data.ExpiresAt) {
			data.ExpiresAt = newExpiry
			payload, _ := json.Marshal(&data)
			ttl := time.Until(newExpiry)
			if ttl > 0 {
				r.client.Set(ctx, key, payload, ttl)
			}
		}
	}
	return &data, nil
}

// Delete implements Store.
func (r *RedisStore) Delete(ctx context.Context, sessionID string) error {
	return r.client.Del(ctx, r.key(sessionID)).Err()
}
