package auth

import "context"

type contextKey struct{}

// User holds stub user identity from metadata (M01).
type User struct {
	ID      string
	AuthSub string
	Name    string
	Email   string
}

// WithUser attaches the user to the context.
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, contextKey{}, u)
}

// FromContext returns the user from the context, or nil.
func FromContext(ctx context.Context) *User {
	u, _ := ctx.Value(contextKey{}).(*User)
	return u
}
