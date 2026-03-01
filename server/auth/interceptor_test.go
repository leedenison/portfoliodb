package auth

import (
	"context"
	"os"
	"testing"

	"github.com/leedenison/portfoliodb/server/db/mock"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// testInterceptorConfig matches production: skip reflection, optional auth for CreateUser.
var testInterceptorConfig = InterceptorConfig{
	SkipAuthPrefixes:    []string{"/grpc.reflection."},
	OptionalAuthMethods: []string{"/portfoliodb.api.v1.ApiService/CreateUser"},
}

func TestUnaryInterceptor_AttachesRoleFromDB(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	userDB := mock.NewMockUserDB(ctrl)
	userDB.EXPECT().
		GetUserByAuthSub(gomock.Any(), "sub|1").
		Return("user-1", "user", nil)
	interceptor := UnaryInterceptor(userDB, testInterceptorConfig)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		metaAuthSub, "sub|1",
		metaName, "Alice",
		metaEmail, "a@b.com",
	))
	var got *User
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/api/ListPortfolios"}, func(ctx context.Context, req interface{}) (interface{}, error) {
		got = FromContext(ctx)
		return nil, nil
	})
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if got == nil || got.ID != "user-1" || got.Role != "user" {
		t.Fatalf("got user %+v", got)
	}
}

func TestUnaryInterceptor_ADMIN_AUTH_SUB_OverridesRole(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	userDB := mock.NewMockUserDB(ctrl)
	userDB.EXPECT().
		GetUserByAuthSub(gomock.Any(), "sub|admin").
		Return("admin-id", "user", nil)
	restore := setEnv("ADMIN_AUTH_SUB", "sub|admin")
	defer restore()
	interceptor := UnaryInterceptor(userDB, testInterceptorConfig)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		metaAuthSub, "sub|admin",
		metaName, "Admin",
		metaEmail, "admin@b.com",
	))
	var got *User
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/api/ListPortfolios"}, func(ctx context.Context, req interface{}) (interface{}, error) {
		got = FromContext(ctx)
		return nil, nil
	})
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if got == nil || got.Role != "admin" {
		t.Fatalf("expected Role=admin, got %+v", got)
	}
}

func TestUnaryInterceptor_SkipAuthPrefix_CallsHandlerWithoutUser(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	userDB := mock.NewMockUserDB(ctrl)
	// No GetUserByAuthSub expected — reflection is skipped
	interceptor := UnaryInterceptor(userDB, testInterceptorConfig)
	ctx := context.Background() // no metadata
	var got *User
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo"}, func(ctx context.Context, req interface{}) (interface{}, error) {
		got = FromContext(ctx)
		return nil, nil
	})
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if got != nil {
		t.Fatalf("expected no user for skip-auth method, got %+v", got)
	}
}

func TestUnaryInterceptor_UnknownUser_NonCreateUser_ReturnsUnauthenticated(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	userDB := mock.NewMockUserDB(ctrl)
	userDB.EXPECT().
		GetUserByAuthSub(gomock.Any(), "sub|unknown").
		Return("", "", nil)
	interceptor := UnaryInterceptor(userDB, testInterceptorConfig)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(metaAuthSub, "sub|unknown"))
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/api/ListPortfolios"}, func(context.Context, interface{}) (interface{}, error) {
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("got %v", err)
	}
}

func TestStreamInterceptor_AttachesUserToContext(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	userDB := mock.NewMockUserDB(ctrl)
	userDB.EXPECT().
		GetUserByAuthSub(gomock.Any(), "sub|1").
		Return("user-1", "user", nil)
	interceptor := StreamInterceptor(userDB, testInterceptorConfig)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		metaAuthSub, "sub|1",
		metaName, "Alice",
		metaEmail, "a@b.com",
	))
	stream := &streamMock{ctx: ctx}
	var got *User
	err := interceptor(nil, stream, &grpc.StreamServerInfo{FullMethod: "/portfoliodb.api.v1.ApiService/ExportInstruments"}, func(srv interface{}, stream grpc.ServerStream) error {
		got = FromContext(stream.Context())
		return nil
	})
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if got == nil || got.ID != "user-1" || got.Role != "user" {
		t.Fatalf("got user %+v", got)
	}
}

func TestStreamInterceptor_MissingAuth_ReturnsUnauthenticated(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	userDB := mock.NewMockUserDB(ctrl)
	interceptor := StreamInterceptor(userDB, testInterceptorConfig)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(metaName, "Alice"))
	stream := &streamMock{ctx: ctx}
	err := interceptor(nil, stream, &grpc.StreamServerInfo{FullMethod: "/portfoliodb.api.v1.ApiService/ExportInstruments"}, func(srv interface{}, stream grpc.ServerStream) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("got %v", err)
	}
}

func TestStreamInterceptor_UnknownUser_ReturnsUnauthenticated(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	userDB := mock.NewMockUserDB(ctrl)
	userDB.EXPECT().
		GetUserByAuthSub(gomock.Any(), "sub|unknown").
		Return("", "", nil)
	interceptor := StreamInterceptor(userDB, testInterceptorConfig)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(metaAuthSub, "sub|unknown"))
	stream := &streamMock{ctx: ctx}
	err := interceptor(nil, stream, &grpc.StreamServerInfo{FullMethod: "/portfoliodb.api.v1.ApiService/ExportInstruments"}, func(srv interface{}, stream grpc.ServerStream) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("got %v", err)
	}
}

// streamMock implements grpc.ServerStream for tests.
type streamMock struct {
	ctx context.Context
}

func (s *streamMock) Context() context.Context       { return s.ctx }
func (s *streamMock) RecvMsg(m interface{}) error    { return nil }
func (s *streamMock) SendMsg(m interface{}) error    { return nil }
func (s *streamMock) SetHeader(m metadata.MD) error  { return nil }
func (s *streamMock) SendHeader(m metadata.MD) error { return nil }
func (s *streamMock) SetTrailer(m metadata.MD)      {}

func setEnv(key, value string) func() {
	old := os.Getenv(key)
	os.Setenv(key, value)
	return func() {
		if old == "" {
			os.Unsetenv(key)
		} else {
			os.Setenv(key, old)
		}
	}
}
