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

func makeUnaryCtx(cookie string) context.Context {
	if cookie == "" {
		return context.Background()
	}
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs("cookie", cookie))
}

func TestUnaryInterceptor(t *testing.T) {
	tests := []struct {
		name                 string
		cookie               string
		fullMethod           string
		wantUnauthenticated  bool
		wantUserInContext   bool
	}{
		{"AttachesUserFromSession", "portfoliodb_session=valid-session-id", "/api/ListPortfolios", false, true},
		{"SkipAuthPrefix_CallsHandlerWithoutUser", "", "/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo", false, false},
		{"NoSessionMethod_CallsHandlerWithoutUser", "", "/portfoliodb.auth.v1.AuthService/Auth", false, false},
		{"MissingSession_ReturnsUnauthenticated", "other=value", "/api/ListPortfolios", true, false},
		{"InvalidSession_ReturnsUnauthenticated", "portfoliodb_session=invalid-id", "/api/ListPortfolios", true, false},
		{"OptionalSession_AllowsMissingSession", "", "/portfoliodb.auth.v1.AuthService/Logout", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			interceptor := UnaryInterceptor(testInterceptorConfig)
			ctx := makeUnaryCtx(tc.cookie)
			var got *User
			_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: tc.fullMethod}, func(ctx context.Context, req interface{}) (interface{}, error) {
				got = FromContext(ctx)
				return nil, nil
			})
			if tc.wantUnauthenticated {
				if err == nil {
					t.Fatal("expected error")
				}
				if status.Code(err) != codes.Unauthenticated {
					t.Fatalf("got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("handler err: %v", err)
			}
			if tc.wantUserInContext {
				if got == nil || got.ID != "user-1" || got.Role != "user" {
					t.Fatalf("got user %+v", got)
				}
			} else if got != nil {
				t.Fatalf("expected no user, got %+v", got)
			}
		})
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
