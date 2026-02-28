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

// CreateUserFullMethod is the gRPC full method name for CreateUser (allowed without existing user).
const CreateUserFullMethod = "/portfoliodb.api.v1.ApiService/CreateUser"

// userFromMetadata reads metadata from ctx, resolves the user via userDB, and returns the User or a gRPC error.
// fullMethod is used to allow unknown users only for CreateUserFullMethod.
func userFromMetadata(ctx context.Context, userDB db.UserDB, fullMethod string) (*User, error) {
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
	if userID == "" && fullMethod != CreateUserFullMethod {
		return nil, status.Error(codes.Unauthenticated, "unknown user: call CreateUser first")
	}
	return u, nil
}

// UnaryInterceptor returns a gRPC unary interceptor that attaches stub user from metadata.
// For M01, expects x-auth-sub, x-auth-name, x-auth-email (or Authorization bearer = auth_sub).
// CreateUser is allowed without a pre-existing user; other RPCs require a known user.
func UnaryInterceptor(userDB db.UserDB) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		u, err := userFromMetadata(ctx, userDB, info.FullMethod)
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

// StreamInterceptor returns a gRPC stream interceptor that attaches stub user from metadata.
// Same auth rules as UnaryInterceptor; stream handlers receive the user via stream.Context().
func StreamInterceptor(userDB db.UserDB) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		u, err := userFromMetadata(ss.Context(), userDB, info.FullMethod)
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
