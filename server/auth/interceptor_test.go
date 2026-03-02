package auth

import (
	"context"
	"testing"
	"time"

	"github.com/leedenison/portfoliodb/server/auth/session"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// testSessionStore returns a valid session for session ID "valid-session-id".
type testSessionStore struct {
	validID string
	data    *session.Data
}

func (t *testSessionStore) Create(ctx context.Context, data *session.Data, maxAge time.Duration) (string, error) {
	return "", nil
}
func (t *testSessionStore) Get(ctx context.Context, sessionID string, slidingWindow time.Duration) (*session.Data, error) {
	if sessionID == t.validID && t.data != nil {
		return t.data, nil
	}
	return nil, nil
}
func (t *testSessionStore) Delete(ctx context.Context, sessionID string) error {
	return nil
}

var testInterceptorConfig = InterceptorConfig{
	SkipAuthPrefixes:       []string{"/grpc.reflection."},
	NoSessionMethods:       []string{"/portfoliodb.auth.v1.AuthService/Auth"},
	OptionalSessionMethods: []string{"/portfoliodb.auth.v1.AuthService/Logout"},
	SessionStore: &testSessionStore{
		validID: "valid-session-id",
		data: &session.Data{
			UserID:    "user-1",
			Email:     "a@b.com",
			GoogleSub: "sub|1",
			Role:      "user",
		},
	},
	SessionCookieName: "portfoliodb_session",
	ExtendTTL:         time.Hour,
}

func TestUnaryInterceptor_AttachesUserFromSession(t *testing.T) {
	interceptor := UnaryInterceptor(testInterceptorConfig)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"cookie", "portfoliodb_session=valid-session-id",
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

func TestUnaryInterceptor_SkipAuthPrefix_CallsHandlerWithoutUser(t *testing.T) {
	interceptor := UnaryInterceptor(testInterceptorConfig)
	ctx := context.Background()
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

func TestUnaryInterceptor_NoSessionMethod_CallsHandlerWithoutUser(t *testing.T) {
	interceptor := UnaryInterceptor(testInterceptorConfig)
	ctx := context.Background()
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/portfoliodb.auth.v1.AuthService/Auth"}, func(ctx context.Context, req interface{}) (interface{}, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
}

func TestUnaryInterceptor_MissingSession_ReturnsUnauthenticated(t *testing.T) {
	interceptor := UnaryInterceptor(testInterceptorConfig)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("cookie", "other=value"))
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

func TestUnaryInterceptor_InvalidSession_ReturnsUnauthenticated(t *testing.T) {
	interceptor := UnaryInterceptor(testInterceptorConfig)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("cookie", "portfoliodb_session=invalid-id"))
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
	interceptor := StreamInterceptor(testInterceptorConfig)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("cookie", "portfoliodb_session=valid-session-id"))
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

func TestStreamInterceptor_MissingSession_ReturnsUnauthenticated(t *testing.T) {
	interceptor := StreamInterceptor(testInterceptorConfig)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("cookie", "other=value"))
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

func TestUnaryInterceptor_OptionalSession_AllowsMissingSession(t *testing.T) {
	interceptor := UnaryInterceptor(testInterceptorConfig)
	ctx := context.Background()
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/portfoliodb.auth.v1.AuthService/Logout"}, func(context.Context, interface{}) (interface{}, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
}

type streamMock struct {
	ctx context.Context
}

func (s *streamMock) Context() context.Context       { return s.ctx }
func (s *streamMock) RecvMsg(m interface{}) error   { return nil }
func (s *streamMock) SendMsg(m interface{}) error   { return nil }
func (s *streamMock) SetHeader(m metadata.MD) error { return nil }
func (s *streamMock) SendHeader(m metadata.MD) error { return nil }
func (s *streamMock) SetTrailer(m metadata.MD)      {}
