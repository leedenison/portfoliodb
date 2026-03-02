package session

import (
	"context"
	"time"
)

// Data holds the session payload.
type Data struct {
	UserID    string
	Email     string
	GoogleSub string
	Role      string
	CreatedAt time.Time
	ExpiresAt time.Time
	LastSeenAt time.Time
}

// Store is the session store interface.
type Store interface {
	// Create stores a new session and returns the session ID (opaque, for cookie value).
	Create(ctx context.Context, data *Data, maxAge time.Duration) (sessionID string, err error)
	// Get loads the session by ID. If sliding is true and the session is valid, Extend is called (e.g. extend 72h).
	Get(ctx context.Context, sessionID string, slidingWindow time.Duration) (*Data, error)
	// Delete removes the session.
	Delete(ctx context.Context, sessionID string) error
}
