package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	authv1 "github.com/leedenison/portfoliodb/proto/auth/v1"
	authpkg "github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/auth/session"
	"github.com/leedenison/portfoliodb/server/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

// The two RPCs either side of a session's life: reading who the caller is, and
// ending it. Both read the cookie, and Logout writes one.

// sessionServer returns a server with the given cookie config and a store, plus the
// transport stream its Set-Cookie headers land in.
func sessionServer(t *testing.T, cookie CookieConfig) (*Server, *stubSessionStore, *stubServerTransportStream) {
	t.Helper()
	store := newStubSessionStore()
	s := NewServer(nil, store, &stubUserDB{userID: "user-1"}, &stubSvcAcctDB{}, nil,
		cookie, 30*24*time.Hour, 72*time.Hour, time.Hour, "")
	return s, store, &stubServerTransportStream{}
}

// withSession returns a context carrying the cookie and the transport stream, as an
// authenticated request has.
func withSession(stream *stubServerTransportStream, cookieName, sessionID string) context.Context {
	ctx := grpc.NewContextWithServerTransportStream(context.Background(), stream)
	if sessionID == "" {
		return ctx
	}
	return metadata.NewIncomingContext(ctx, metadata.Pairs("cookie", cookieName+"="+sessionID))
}

// setCookie returns the single Set-Cookie header the call sent.
func setCookie(t *testing.T, stream *stubServerTransportStream) string {
	t.Helper()
	got := stream.headers.Get("set-cookie")
	if len(got) != 1 {
		t.Fatalf("set-cookie headers: got %d (%v), want 1", len(got), got)
	}
	return got[0]
}

// GetSession answers from the user the interceptor put in the context, which is the
// only thing that says the session was valid. It reads the id back off the cookie
// rather than holding one, so the answer names the session it was asked about.
func TestGetSession(t *testing.T) {
	cookie := CookieConfig{Name: "portfoliodb_session", Path: "/"}
	s, _, stream := sessionServer(t, cookie)
	ctx := withSession(stream, cookie.Name, "sess-1")
	ctx = authpkg.WithUser(ctx, &authpkg.User{
		ID: "user-1", Email: "u@example.com", Name: "U", Role: "admin",
	})

	resp, err := s.GetSession(ctx, &authv1.GetSessionRequest{})
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got := resp.GetUser(); got.GetId() != "user-1" || got.GetEmail() != "u@example.com" ||
		got.GetName() != "U" || got.GetRole() != "admin" {
		t.Errorf("user: got %+v", got)
	}
	if !resp.GetUserExists() {
		t.Error("user_exists: got false, want true -- a session implies the user is there")
	}
	if got := resp.GetSession().GetSessionId(); got != "sess-1" {
		t.Errorf("session id: got %q, want the one the cookie carried", got)
	}
}

// No user in the context means the interceptor did not accept the request, whatever
// the caller sent. A cookie naming a session that does not exist reaches here the
// same way one naming nothing does.
func TestGetSession_Unauthenticated(t *testing.T) {
	cookie := CookieConfig{Name: "portfoliodb_session", Path: "/"}
	s, _, stream := sessionServer(t, cookie)
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{"no user and no cookie", withSession(stream, cookie.Name, "")},
		{"a cookie the interceptor did not accept", withSession(stream, cookie.Name, "sess-1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.GetSession(tt.ctx, &authv1.GetSessionRequest{})
			testutil.RequireGRPCCode(t, err, codes.Unauthenticated)
		})
	}
}

// Logout destroys the session and tells the browser to drop the cookie.
func TestLogout(t *testing.T) {
	cookie := CookieConfig{Name: "portfoliodb_session", Path: "/"}
	s, store, stream := sessionServer(t, cookie)
	store.sessions["sess-1"] = &session.Data{Kind: session.SessionKindUser, UserID: "user-1"}

	if _, err := s.Logout(withSession(stream, cookie.Name, "sess-1"), &authv1.LogoutRequest{}); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, still := store.sessions["sess-1"]; still {
		t.Error("the session survived logout")
	}
	if got := setCookie(t, stream); !strings.Contains(got, "Max-Age=0") {
		t.Errorf("set-cookie: got %q, want it to clear the cookie", got)
	}
}

// A logout with no session still clears the cookie. The cookie is the thing the
// browser holds, so a request that arrives without a usable one is exactly when
// clearing it matters.
func TestLogout_NoSession(t *testing.T) {
	cookie := CookieConfig{Name: "portfoliodb_session", Path: "/"}
	s, _, stream := sessionServer(t, cookie)

	if _, err := s.Logout(withSession(stream, cookie.Name, ""), &authv1.LogoutRequest{}); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if got := setCookie(t, stream); !strings.Contains(got, "Max-Age=0") {
		t.Errorf("set-cookie: got %q, want it to clear the cookie", got)
	}
}

// failingDeleteStore is a store whose Delete always fails, for the case where the
// session cannot be destroyed.
type failingDeleteStore struct{ *stubSessionStore }

func (failingDeleteStore) Delete(context.Context, string) error {
	return errors.New("redis unavailable")
}

// A store that cannot delete does not leave the caller logged in at the browser. The
// session may outlive the request, but the cookie is cleared and the error is not
// returned -- there is nothing the user can do about it and refusing would leave
// them holding a cookie they asked to be rid of.
func TestLogout_StoreFailureStillClearsTheCookie(t *testing.T) {
	cookie := CookieConfig{Name: "portfoliodb_session", Path: "/"}
	store := newStubSessionStore()
	s := NewServer(nil, failingDeleteStore{store}, &stubUserDB{}, &stubSvcAcctDB{}, nil,
		cookie, time.Hour, time.Hour, time.Hour, "")
	stream := &stubServerTransportStream{}

	if _, err := s.Logout(withSession(stream, cookie.Name, "sess-1"), &authv1.LogoutRequest{}); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if got := setCookie(t, stream); !strings.Contains(got, "Max-Age=0") {
		t.Errorf("set-cookie: got %q, want it to clear the cookie", got)
	}
}

// The cleared cookie has to match the one that was set closely enough for the
// browser to replace it rather than add a second: same name and path. It carries no
// value and expires at once, and stays HttpOnly so the clearing is not itself
// scriptable.
func TestClearSessionCookie(t *testing.T) {
	tests := []struct {
		name   string
		cookie CookieConfig
		want   string
	}{
		{
			name:   "the ordinary one",
			cookie: CookieConfig{Name: "portfoliodb_session", Path: "/"},
			want:   "portfoliodb_session=; Path=/; Max-Age=0; HttpOnly",
		},
		{
			// Secure is carried over, because a cookie set with it is a different
			// cookie from one set without and would not be replaced.
			name:   "a secure one",
			cookie: CookieConfig{Name: "portfoliodb_session", Path: "/", Secure: true},
			want:   "portfoliodb_session=; Path=/; Max-Age=0; HttpOnly; Secure",
		},
		{
			name:   "a scoped path",
			cookie: CookieConfig{Name: "sid", Path: "/app"},
			want:   "sid=; Path=/app; Max-Age=0; HttpOnly",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := &stubServerTransportStream{}
			ctx := grpc.NewContextWithServerTransportStream(context.Background(), stream)
			clearSessionCookie(ctx, tt.cookie)
			if got := setCookie(t, stream); got != tt.want {
				t.Errorf("set-cookie:\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}
