package auth

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RequireUser returns the user from ctx, or a gRPC Unauthenticated error if missing.
func RequireUser(ctx context.Context) (*User, error) {
	u := FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	return u, nil
}

// RequireAdmin returns the user from ctx if they have the admin role, or a gRPC error (Unauthenticated or PermissionDenied).
func RequireAdmin(ctx context.Context) (*User, error) {
	u, err := RequireUser(ctx)
	if err != nil {
		return nil, err
	}
	if u.Role != "admin" {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}
	return u, nil
}
