package auth

import (
	"context"
	"strings"
	"time"

	"github.com/leedenison/portfoliodb/server/auth/session"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// InterceptorConfig declares which methods skip auth, which require no session (e.g. Auth), which allow optional session (e.g. Logout), and session store.
type InterceptorConfig struct {
	// SkipAuthPrefixes: fullMethod with any of these prefixes is not authenticated (e.g. reflection).
	SkipAuthPrefixes []string
	// NoSessionMethods: fullMethod in this list does not require a session (e.g. AuthService.Auth).
	NoSessionMethods []string
	// OptionalSessionMethods: fullMethod in this list allows missing session (e.g. AuthService.Logout).
	OptionalSessionMethods []string
	SessionStore           session.Store
	SessionCookieName      string
	ExtendTTL              time.Duration
}

func matchesPrefix(fullMethod string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(fullMethod, p) {
			return true
		}
	}
	return false
}

func inList(fullMethod string, list []string) bool {
	for _, m := range list {
		if fullMethod == m {
			return true
		}
	}
	return false
}

// sessionIDFromMetadata returns the session ID from incoming metadata (Cookie header or Authorization: Bearer).
func sessionIDFromMetadata(ctx context.Context, cookieName string) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	for _, v := range md.Get("cookie") {
		for _, part := range strings.Split(v, ";") {
			part = strings.TrimSpace(part)
			if eq := strings.IndexByte(part, '='); eq > 0 && strings.TrimSpace(part[:eq]) == cookieName {
				return strings.TrimSpace(part[eq+1:])
			}
		}
	}
	for _, v := range md.Get("authorization") {
		if strings.HasPrefix(v, "Bearer ") {
			return strings.TrimPrefix(v, "Bearer ")
		}
	}
	return ""
}

// identityFromSession loads the session and attaches the appropriate identity (User or ServiceAccount) to the context.
// Returns (ctx, found, error).
func identityFromSession(ctx context.Context, cfg InterceptorConfig) (context.Context, bool, error) {
	sessionID := sessionIDFromMetadata(ctx, cfg.SessionCookieName)
	if sessionID == "" {
		return ctx, false, nil
	}
	data, err := cfg.SessionStore.Get(ctx, sessionID, cfg.ExtendTTL)
	if err != nil || data == nil {
		return ctx, false, nil
	}
	switch data.Kind {
	case session.SessionKindServiceAccount:
		ctx = WithServiceAccount(ctx, &ServiceAccount{
			ID:   data.ServiceAccountID,
			Role: data.Role,
		})
	default: // SessionKindUser or empty (backwards compat)
		ctx = WithUser(ctx, &User{
			ID:      data.UserID,
			AuthSub: data.GoogleSub,
			Email:   data.Email,
			Name:    data.Email, // session may not store name; email is fine for display
			Role:    data.Role,
		})
	}
	return ctx, true, nil
}

// UnaryInterceptor returns a gRPC unary interceptor that attaches user from session cookie.
func UnaryInterceptor(cfg InterceptorConfig) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if matchesPrefix(info.FullMethod, cfg.SkipAuthPrefixes) {
			return handler(ctx, req)
		}
		if inList(info.FullMethod, cfg.NoSessionMethods) {
			return handler(ctx, req)
		}
		ctx, found, err := identityFromSession(ctx, cfg)
		if err != nil {
			return nil, err
		}
		if !found {
			if inList(info.FullMethod, cfg.OptionalSessionMethods) {
				return handler(ctx, req)
			}
			return nil, status.Error(codes.Unauthenticated, "missing or invalid session")
		}
		return handler(ctx, req)
	}
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context {
	return w.ctx
}

// StreamInterceptor returns a gRPC stream interceptor that attaches user from session cookie.
func StreamInterceptor(cfg InterceptorConfig) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if matchesPrefix(info.FullMethod, cfg.SkipAuthPrefixes) {
			return handler(srv, ss)
		}
		if inList(info.FullMethod, cfg.NoSessionMethods) {
			return handler(srv, ss)
		}
		ctx, found, err := identityFromSession(ss.Context(), cfg)
		if err != nil {
			return err
		}
		if !found {
			if inList(info.FullMethod, cfg.OptionalSessionMethods) {
				return handler(srv, ss)
			}
			return status.Error(codes.Unauthenticated, "missing or invalid session")
		}
		wrapped := &wrappedStream{ServerStream: ss, ctx: ctx}
		return handler(srv, wrapped)
	}
}
