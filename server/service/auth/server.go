package auth

import (
	"context"
	"fmt"
	"net/http"
	"time"

	authv1 "github.com/leedenison/portfoliodb/proto/auth/v1"
	"github.com/leedenison/portfoliodb/server/auth/allowlist"
	"github.com/leedenison/portfoliodb/server/auth/google"
	"github.com/leedenison/portfoliodb/server/auth/session"
	"github.com/leedenison/portfoliodb/server/db"
	authpkg "github.com/leedenison/portfoliodb/server/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// CookieConfig configures the session cookie.
type CookieConfig struct {
	Name     string
	Path     string
	MaxAge   int
	Secure   bool
	SameSite string
}

// Server implements AuthService.
type Server struct {
	authv1.UnimplementedAuthServiceServer
	verifier     *google.Verifier
	sessionStore session.Store
	userDB       db.UserDB
	allowlist    *allowlist.Matcher
	cookie       CookieConfig
	sessionTTL   time.Duration
	extendTTL    time.Duration
	adminAuthSub string
}

// NewServer returns a new Auth server.
func NewServer(
	verifier *google.Verifier,
	sessionStore session.Store,
	userDB db.UserDB,
	allowlist *allowlist.Matcher,
	cookie CookieConfig,
	sessionTTL time.Duration,
	extendTTL time.Duration,
	adminAuthSub string,
) *Server {
	if cookie.Name == "" {
		cookie.Name = "portfoliodb_session"
	}
	if cookie.Path == "" {
		cookie.Path = "/"
	}
	return &Server{
		verifier:     verifier,
		sessionStore: sessionStore,
		userDB:       userDB,
		allowlist:    allowlist,
		cookie:       cookie,
		sessionTTL:   sessionTTL,
		extendTTL:    extendTTL,
		adminAuthSub: adminAuthSub,
	}
}

// Auth verifies the Google ID token, checks allowlist, provisions user, creates session, sets cookie.
func (s *Server) Auth(ctx context.Context, req *authv1.AuthRequest) (*authv1.AuthResponse, error) {
	token := req.GetGoogleIdToken()
	if token == "" {
		return nil, status.Error(codes.InvalidArgument, "missing google_id_token")
	}
	result, err := s.verifier.Verify(ctx, token)
	if err != nil {
		switch {
		case err == google.ErrInvalidArgument:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case err == google.ErrPermissionDenied:
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Error(codes.Unauthenticated, err.Error())
		}
	}
	if s.allowlist != nil && !s.allowlist.Match(result.Email) {
		return nil, status.Error(codes.PermissionDenied, "email not allowlisted")
	}
	userID, userExists, err := s.provisionUser(ctx, result.Sub, result.Email, result.Name)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	role := "user"
	if result.Sub == s.adminAuthSub {
		role = "admin"
	}
	sessData := &session.Data{
		UserID:    userID,
		Email:     result.Email,
		GoogleSub: result.Sub,
		Role:      role,
	}
	sessionID, err := s.sessionStore.Create(ctx, sessData, s.sessionTTL)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := sendSetCookieHeader(ctx, s.cookie, sessionID); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &authv1.AuthResponse{
		User: &authv1.User{
			Id:    userID,
			Email: result.Email,
			Name:  result.Name,
			Role:  role,
		},
		UserExists: userExists,
		SessionId:  sessionID, // for programmatic clients (e.g. scripts) that cannot use cookies
	}, nil
}

// provisionUser implements auth.md: lookup by google_sub, then by email (bind), else create. Returns userID, userExists, error.
func (s *Server) provisionUser(ctx context.Context, googleSub, email, name string) (string, bool, error) {
	userID, _, err := s.userDB.GetUserByAuthSub(ctx, googleSub)
	if err != nil {
		return "", false, err
	}
	if userID != "" {
		return userID, true, nil
	}
	existingID, err := s.userDB.GetUserByEmail(ctx, email)
	if err != nil {
		return "", false, err
	}
	if existingID != "" {
		if err := s.userDB.UpdateUserAuthSub(ctx, existingID, googleSub); err != nil {
			return "", false, err
		}
		return existingID, true, nil
	}
	userID, err = s.userDB.GetOrCreateUser(ctx, googleSub, name, email)
	if err != nil {
		return "", false, err
	}
	return userID, false, nil
}

// GetSession returns the current user when the request has a valid session cookie.
func (s *Server) GetSession(ctx context.Context, _ *emptypb.Empty) (*authv1.AuthResponse, error) {
	u := authpkg.FromContext(ctx)
	if u == nil {
		return nil, status.Error(codes.Unauthenticated, "missing or invalid session")
	}
	return &authv1.AuthResponse{
		User: &authv1.User{
			Id:    u.ID,
			Email: u.Email,
			Name:  u.Name,
			Role:  u.Role,
		},
		UserExists: true, // session implies existing user
		SessionId:  getSessionIDFromContext(ctx, s.cookie.Name),
	}, nil
}

// Logout deletes the session and clears the cookie.
func (s *Server) Logout(ctx context.Context, _ *emptypb.Empty) (*emptypb.Empty, error) {
	sessionID := getSessionIDFromContext(ctx, s.cookie.Name)
	if sessionID != "" {
		_ = s.sessionStore.Delete(ctx, sessionID)
	}
	clearSessionCookie(ctx, s.cookie)
	return &emptypb.Empty{}, nil
}

// sendSetCookieHeader sends Set-Cookie via gRPC response header (for gRPC-Web gateway to pass through).
func sendSetCookieHeader(ctx context.Context, c CookieConfig, sessionID string) error {
	value := fmt.Sprintf("%s=%s; Path=%s; Max-Age=%d; HttpOnly", c.Name, sessionID, c.Path, c.MaxAge)
	if c.Secure {
		value += "; Secure"
	}
	if c.SameSite != "" {
		value += "; SameSite=" + c.SameSite
	}
	return grpc.SendHeader(ctx, metadata.Pairs("set-cookie", value))
}

// clearSessionCookie sends Set-Cookie with Max-Age=0 to clear the cookie.
func clearSessionCookie(ctx context.Context, c CookieConfig) {
	value := fmt.Sprintf("%s=; Path=%s; Max-Age=0; HttpOnly", c.Name, c.Path)
	if c.Secure {
		value += "; Secure"
	}
	_ = grpc.SendHeader(ctx, metadata.Pairs("set-cookie", value))
}

// SessionIDFromContext returns the session ID from incoming metadata (cookie). Used by interceptor and Logout.
func SessionIDFromContext(ctx context.Context, cookieName string) string {
	return getSessionIDFromContext(ctx, cookieName)
}

func getSessionIDFromContext(ctx context.Context, cookieName string) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get("cookie")
	for _, v := range vals {
		cookies := readCookies(v, cookieName)
		for _, c := range cookies {
			if c.Name == cookieName {
				return c.Value
			}
		}
	}
	vals = md.Get("x-session-id")
	if len(vals) > 0 && vals[0] != "" {
		return vals[0]
	}
	return ""
}

// Minimal cookie parsing for "name=value" in Cookie header.
func readCookies(header, filter string) []*http.Cookie {
	var out []*http.Cookie
	for _, part := range splitCookie(header) {
		eq := indexByte(part, '=')
		if eq < 0 {
			continue
		}
		name := trimSpace(part[:eq])
		value := trimSpace(part[eq+1:])
		if filter != "" && name != filter {
			continue
		}
		out = append(out, &http.Cookie{Name: name, Value: value})
	}
	return out
}

func splitCookie(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ';' {
			parts = append(parts, trimSpace(s[start:i]))
			start = i + 1
		}
	}
	if start < len(s) {
		parts = append(parts, trimSpace(s[start:]))
	}
	return parts
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
