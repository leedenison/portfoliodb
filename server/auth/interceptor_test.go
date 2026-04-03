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

// testSessionStore returns sessions by ID lookup.
type testSessionStore struct {
	sessions map[string]*session.Data
}

func (t *testSessionStore) Create(ctx context.Context, data *session.Data, maxAge time.Duration) (string, error) {
	return "", nil
}
func (t *testSessionStore) Get(ctx context.Context, sessionID string, slidingWindow time.Duration) (*session.Data, error) {
	if data, ok := t.sessions[sessionID]; ok {
		return data, nil
	}
	return nil, nil
}
func (t *testSessionStore) Delete(ctx context.Context, sessionID string) error {
	return nil
}

var testInterceptorConfig = InterceptorConfig{
	SkipAuthPrefixes: []string{"/grpc.reflection."},
	NoSessionMethods: []string{
		"/portfoliodb.auth.v1.AuthService/AuthUser",
		"/portfoliodb.auth.v1.AuthService/AuthMachine",
	},
	OptionalSessionMethods: []string{"/portfoliodb.auth.v1.AuthService/Logout"},
	SessionStore: &testSessionStore{
		sessions: map[string]*session.Data{
			"valid-user-session": {
				Kind:      session.SessionKindUser,
				UserID:    "user-1",
				Email:     "a@b.com",
				GoogleSub: "sub|1",
				Role:      "user",
			},
			"valid-sa-session": {
				Kind:             session.SessionKindServiceAccount,
				ServiceAccountID: "sa-1",
				Role:             "service_account",
			},
		},
	},
	SessionCookieName: "portfoliodb_session",
	ExtendTTL:         time.Hour,
}

func makeUnaryCtx(cookie, bearer string) context.Context {
	if cookie == "" && bearer == "" {
		return context.Background()
	}
	var pairs []string
	if cookie != "" {
		pairs = append(pairs, "cookie", cookie)
	}
	if bearer != "" {
		pairs = append(pairs, "authorization", "Bearer "+bearer)
	}
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(pairs...))
}

func TestUnaryInterceptor(t *testing.T) {
	tests := []struct {
		name                string
		cookie              string
		bearer              string
		fullMethod          string
		wantUnauthenticated bool
		wantUserInContext   bool
		wantSAInContext     bool
	}{
		{"AttachesUserFromCookie", "portfoliodb_session=valid-user-session", "", "/api/ListPortfolios", false, true, false},
		{"BearerToken_AttachesUser", "", "valid-user-session", "/api/ListPortfolios", false, true, false},
		{"BearerToken_AttachesServiceAccount", "", "valid-sa-session", "/api/ListPortfolios", false, false, true},
		{"BearerToken_Invalid", "", "bad-token", "/api/ListPortfolios", true, false, false},
		{"XSessionId_Rejected", "", "", "/api/ListPortfolios", true, false, false},
		{"SkipAuthPrefix_CallsHandlerWithoutUser", "", "", "/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo", false, false, false},
		{"NoSessionMethod_AuthUser", "", "", "/portfoliodb.auth.v1.AuthService/AuthUser", false, false, false},
{"AuthMachine_NoSessionRequired", "", "", "/portfoliodb.auth.v1.AuthService/AuthMachine", false, false, false},
		{"MissingSession_ReturnsUnauthenticated", "other=value", "", "/api/ListPortfolios", true, false, false},
		{"InvalidSession_ReturnsUnauthenticated", "portfoliodb_session=invalid-id", "", "/api/ListPortfolios", true, false, false},
		{"OptionalSession_AllowsMissingSession", "", "", "/portfoliodb.auth.v1.AuthService/Logout", false, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			interceptor := UnaryInterceptor(testInterceptorConfig)
			ctx := makeUnaryCtx(tc.cookie, tc.bearer)
			// For XSessionId_Rejected, inject x-session-id header
			if tc.name == "XSessionId_Rejected" {
				ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-session-id", "valid-user-session"))
			}
			var gotUser *User
			var gotSA *ServiceAccount
			_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: tc.fullMethod}, func(ctx context.Context, req interface{}) (interface{}, error) {
				gotUser = FromContext(ctx)
				gotSA = ServiceAccountFromContext(ctx)
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
				if gotUser == nil || gotUser.ID != "user-1" || gotUser.Role != "user" {
					t.Fatalf("got user %+v", gotUser)
				}
			}
			if tc.wantSAInContext {
				if gotSA == nil || gotSA.ID != "sa-1" || gotSA.Role != "service_account" {
					t.Fatalf("got service account %+v", gotSA)
				}
			}
			if !tc.wantUserInContext && !tc.wantSAInContext {
				if gotUser != nil {
					t.Fatalf("expected no user, got %+v", gotUser)
				}
				if gotSA != nil {
					t.Fatalf("expected no service account, got %+v", gotSA)
				}
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

func TestStreamInterceptor_BearerToken_AttachesServiceAccount(t *testing.T) {
	interceptor := StreamInterceptor(testInterceptorConfig)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer valid-sa-session"))
	stream := &streamMock{ctx: ctx}
	var gotSA *ServiceAccount
	err := interceptor(nil, stream, &grpc.StreamServerInfo{FullMethod: "/portfoliodb.api.v1.ApiService/ExportInstruments"}, func(srv interface{}, ss grpc.ServerStream) error {
		gotSA = ServiceAccountFromContext(ss.Context())
		return nil
	})
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if gotSA == nil || gotSA.ID != "sa-1" {
		t.Fatalf("got service account %+v", gotSA)
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
