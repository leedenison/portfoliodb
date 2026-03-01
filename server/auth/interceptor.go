package auth

import (
	"context"
	"os"
	"strings"

	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	metaAuthSub = "x-auth-sub"
	metaName    = "x-auth-name"
	metaEmail   = "x-auth-email"
)

// InterceptorConfig declares which methods skip auth, which allow optional auth (attach user if present, allow unknown), and which require a known user.
type InterceptorConfig struct {
	// SkipAuthPrefixes: fullMethod with any of these prefixes is not authenticated; no user is attached to context.
	SkipAuthPrefixes []string
	// OptionalAuthMethods: fullMethod in this list requires auth metadata but allows userID to be empty (e.g. CreateUser).
	OptionalAuthMethods []string
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

// userFromMetadata reads metadata from ctx, resolves the user via userDB, and returns the User or a gRPC error.
// If allowEmptyUser is true, unknown users (userID == "") are allowed; otherwise they get Unauthenticated.
func userFromMetadata(ctx context.Context, userDB db.UserDB, allowEmptyUser bool) (*User, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	authSub := first(md, metaAuthSub)
	name := first(md, metaName)
	email := first(md, metaEmail)
	if authSub == "" {
		if a := first(md, "authorization"); a != "" {
			if strings.HasPrefix(strings.ToLower(a), "bearer ") {
				authSub = strings.TrimSpace(a[7:])
			}
		}
	}
	if authSub == "" {
		return nil, status.Error(codes.Unauthenticated, "missing x-auth-sub or bearer token")
	}
	userID, role, err := userDB.GetUserByAuthSub(ctx, authSub)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if authSub != "" && authSub == os.Getenv("ADMIN_AUTH_SUB") {
		role = "admin"
	}
	u := &User{AuthSub: authSub, Name: name, Email: email, ID: userID, Role: role}
	if userID == "" && !allowEmptyUser {
		return nil, status.Error(codes.Unauthenticated, "unknown user: call CreateUser first")
	}
	return u, nil
}

// UnaryInterceptor returns a gRPC unary interceptor that attaches stub user from metadata according to cfg.
// For M01, expects x-auth-sub, x-auth-name, x-auth-email (or Authorization bearer = auth_sub).
func UnaryInterceptor(userDB db.UserDB, cfg InterceptorConfig) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if matchesPrefix(info.FullMethod, cfg.SkipAuthPrefixes) {
			return handler(ctx, req)
		}
		allowEmpty := inList(info.FullMethod, cfg.OptionalAuthMethods)
		u, err := userFromMetadata(ctx, userDB, allowEmpty)
		if err != nil {
			return nil, err
		}
		ctx = WithUser(ctx, u)
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

// StreamInterceptor returns a gRPC stream interceptor that attaches stub user from metadata according to cfg.
// Same auth rules as UnaryInterceptor; stream handlers receive the user via stream.Context().
func StreamInterceptor(userDB db.UserDB, cfg InterceptorConfig) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if matchesPrefix(info.FullMethod, cfg.SkipAuthPrefixes) {
			return handler(srv, ss)
		}
		allowEmpty := inList(info.FullMethod, cfg.OptionalAuthMethods)
		u, err := userFromMetadata(ss.Context(), userDB, allowEmpty)
		if err != nil {
			return err
		}
		wrapped := &wrappedStream{ServerStream: ss, ctx: WithUser(ss.Context(), u)}
		return handler(srv, wrapped)
	}
}

func first(md metadata.MD, key string) string {
	v := md.Get(key)
	if len(v) == 0 {
		return ""
	}
	return v[0]
}
