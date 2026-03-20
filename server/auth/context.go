package auth

import "context"

type contextKey struct{}
type saContextKey struct{}

type User struct {
	ID      string
	AuthSub string
	Name    string
	Email   string
	Role    string
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

// ServiceAccount holds service account identity from a machine session.
type ServiceAccount struct {
	ID   string
	Role string
}

// WithServiceAccount attaches the service account to the context.
func WithServiceAccount(ctx context.Context, sa *ServiceAccount) context.Context {
	return context.WithValue(ctx, saContextKey{}, sa)
}

// ServiceAccountFromContext returns the service account from the context, or nil.
func ServiceAccountFromContext(ctx context.Context) *ServiceAccount {
	sa, _ := ctx.Value(saContextKey{}).(*ServiceAccount)
	return sa
}
