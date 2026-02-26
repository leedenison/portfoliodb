package auth

import (
	"context"
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

// UnaryInterceptor returns a gRPC unary interceptor that attaches stub user from metadata.
// For M01, expects x-auth-sub, x-auth-name, x-auth-email (or Authorization bearer = auth_sub).
// CreateUser is allowed without a pre-existing user; other RPCs require a known user.
func UnaryInterceptor(userDB db.UserDB) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
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
		userID, err := userDB.GetUserByAuthSub(ctx, authSub)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		u := &User{AuthSub: authSub, Name: name, Email: email, ID: userID}
		if userID == "" && info.FullMethod != CreateUserFullMethod {
			return nil, status.Error(codes.Unauthenticated, "unknown user: call CreateUser first")
		}
		ctx = WithUser(ctx, u)
		return handler(ctx, req)
	}
}

func first(md metadata.MD, key string) string {
	v := md.Get(key)
	if len(v) == 0 {
		return ""
	}
	return v[0]
}
