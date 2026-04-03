package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authv1 "github.com/leedenison/portfoliodb/proto/auth/v1"
	"github.com/leedenison/portfoliodb/server/auth/allowlist"
	"github.com/leedenison/portfoliodb/server/auth/google"
	"github.com/leedenison/portfoliodb/server/auth/session"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// stubServerTransportStream implements grpc.ServerTransportStream for tests
// that call methods which use grpc.SendHeader.
type stubServerTransportStream struct {
	headers metadata.MD
}

func (s *stubServerTransportStream) Method() string                  { return "" }
func (s *stubServerTransportStream) SetHeader(md metadata.MD) error  { s.headers = metadata.Join(s.headers, md); return nil }
func (s *stubServerTransportStream) SendHeader(md metadata.MD) error { s.headers = metadata.Join(s.headers, md); return nil }
func (s *stubServerTransportStream) SetTrailer(md metadata.MD) error { return nil }

func grpcContext() context.Context {
	return grpc.NewContextWithServerTransportStream(context.Background(), &stubServerTransportStream{})
}

// testRSAKey is a test RSA key pair generated once for all tests in this package.
var testRSAKey *rsa.PrivateKey

func init() {
	var err error
	testRSAKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("failed to generate test RSA key: " + err.Error())
	}
}

// makeJWKSServer returns an httptest.Server serving a JWKS with the given key.
func makeJWKSServer(t *testing.T, key *rsa.PublicKey, kid string) *httptest.Server {
	t.Helper()
	nBytes := key.N.Bytes()
	nB64 := base64.RawURLEncoding.EncodeToString(nBytes)
	eBytes := big.NewInt(int64(key.E)).Bytes()
	eB64 := base64.RawURLEncoding.EncodeToString(eBytes)
	jwks := map[string]interface{}{
		"keys": []map[string]interface{}{
			{"kty": "RSA", "alg": "RS256", "kid": kid, "n": nB64, "e": eB64},
		},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	}))
}

// signToken creates a signed JWT with the given claims.
func signToken(t *testing.T, key *rsa.PrivateKey, kid, clientID, sub, email string, emailVerified bool, exp time.Time) string {
	t.Helper()
	verified := emailVerified
	claims := google.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://accounts.google.com",
			Subject:   sub,
			Audience:  jwt.ClaimStrings{clientID},
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Email:         email,
		EmailVerified: &verified,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

// stubSessionStore is a minimal in-memory session store for tests.
type stubSessionStore struct {
	sessions map[string]*session.Data
}

func newStubSessionStore() *stubSessionStore {
	return &stubSessionStore{sessions: make(map[string]*session.Data)}
}

func (s *stubSessionStore) Create(_ context.Context, data *session.Data, maxAge time.Duration) (string, error) {
	id := "test-session-id"
	cp := *data
	cp.CreatedAt = time.Now()
	cp.ExpiresAt = cp.CreatedAt.Add(maxAge)
	cp.LastSeenAt = cp.CreatedAt
	s.sessions[id] = &cp
	return id, nil
}

func (s *stubSessionStore) Get(_ context.Context, id string, _ time.Duration) (*session.Data, error) {
	return s.sessions[id], nil
}

func (s *stubSessionStore) Delete(_ context.Context, id string) error {
	delete(s.sessions, id)
	return nil
}

// stubUserDB is a minimal UserDB that always creates new users.
type stubUserDB struct {
	userID string
}

func (s *stubUserDB) GetOrCreateUser(_ context.Context, authSub, name, email string) (string, error) {
	return s.userID, nil
}

func (s *stubUserDB) GetUserByAuthSub(_ context.Context, authSub string) (string, string, error) {
	return "", "", nil
}

func (s *stubUserDB) GetUserByEmail(_ context.Context, email string) (string, error) {
	return "", nil
}

func (s *stubUserDB) UpdateUserAuthSub(_ context.Context, userID, authSub string) error {
	return nil
}

func (s *stubUserDB) GetDisplayCurrency(_ context.Context, userID string) (string, error) {
	return "USD", nil
}

func (s *stubUserDB) SetDisplayCurrency(_ context.Context, userID, currency string) error {
	return nil
}

// stubSvcAcctDB is a minimal ServiceAccountDB for tests.
type stubSvcAcctDB struct {
	row *db.ServiceAccountRow
}

func (s *stubSvcAcctDB) GetServiceAccount(_ context.Context, id string) (*db.ServiceAccountRow, error) {
	if s.row != nil && s.row.ID == id {
		return s.row, nil
	}
	return nil, nil
}

// newTestServer builds a Server wired to a stub JWKS server for the given clientID.
func newTestServer(t *testing.T, clientID string, svcAcctDB db.ServiceAccountDB, al *allowlist.Matcher) (*Server, *stubSessionStore) {
	t.Helper()
	srv := makeJWKSServer(t, &testRSAKey.PublicKey, "test-kid")
	t.Cleanup(srv.Close)

	verifier := google.NewVerifier(clientID,
		google.WithHTTPClient(srv.Client()),
		google.WithJWKSCacheTTL(time.Minute),
	)
	// Point the verifier at our stub JWKS server by overriding the URL via a custom HTTP transport.
	// We need to redirect the JWKS URL fetch to our test server.
	verifier = google.NewVerifier(clientID,
		google.WithHTTPClient(newRedirectClient(srv.URL)),
		google.WithJWKSCacheTTL(time.Minute),
		google.WithClockSkew(time.Minute),
	)

	sessStore := newStubSessionStore()
	s := NewServer(
		verifier,
		sessStore,
		&stubUserDB{userID: "user-1"},
		svcAcctDB,
		al,
		CookieConfig{Name: "portfoliodb_session", Path: "/", MaxAge: 86400},
		30*24*time.Hour,
		72*time.Hour,
		time.Hour,
		"",
	)
	return s, sessStore
}

// newRedirectClient returns an HTTP client that redirects all requests to baseURL.
func newRedirectClient(baseURL string) *http.Client {
	return &http.Client{
		Transport: &redirectTransport{baseURL: baseURL},
	}
}

type redirectTransport struct {
	baseURL string
}

func (r *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = "http"
	req2.URL.Host = r.baseURL[len("http://"):]
	req2.URL.Path = req.URL.Path
	return http.DefaultTransport.RoundTrip(req2)
}

func TestAuthUser_MissingToken_InvalidArgument(t *testing.T) {
	s, _ := newTestServer(t, "test-client", &stubSvcAcctDB{}, nil)
	_, err := s.AuthUser(context.Background(), &authv1.AuthUserRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestAuthUser_ValidToken_ReturnsSession(t *testing.T) {
	s, _ := newTestServer(t, "test-client", &stubSvcAcctDB{}, nil)
	tok := signToken(t, testRSAKey, "test-kid", "test-client", "sub|1", "user@test.com", true, time.Now().Add(time.Hour))
	resp, err := s.AuthUser(grpcContext(), &authv1.AuthUserRequest{GoogleIdToken: tok})
	if err != nil {
		t.Fatalf("AuthUser: %v", err)
	}
	if resp.Session == nil || resp.Session.SessionId == "" {
		t.Fatal("expected non-empty session_id")
	}
	if resp.Session.ExpiresAt == nil {
		t.Fatal("expected non-nil expires_at")
	}
	if resp.Session.ExpiresAt.AsTime().Before(time.Now()) {
		t.Fatal("expires_at should be in the future")
	}
}

func TestAuthUser_AllowlistBlocked_PermissionDenied(t *testing.T) {
	al, err := allowlist.NewMatcher([]string{"allowed@test.com"}, allowlist.ModeGlob, false)
	if err != nil {
		t.Fatalf("allowlist: %v", err)
	}
	s, _ := newTestServer(t, "test-client", &stubSvcAcctDB{}, al)
	tok := signToken(t, testRSAKey, "test-kid", "test-client", "sub|1", "blocked@test.com", true, time.Now().Add(time.Hour))
	_, err = s.AuthUser(context.Background(), &authv1.AuthUserRequest{GoogleIdToken: tok})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}
}

func TestAuthMachine_MissingClientID_InvalidArgument(t *testing.T) {
	s, _ := newTestServer(t, "test-client", &stubSvcAcctDB{}, nil)
	_, err := s.AuthMachine(context.Background(), &authv1.AuthMachineRequest{ClientSecret: "secret"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestAuthMachine_MissingClientSecret_InvalidArgument(t *testing.T) {
	s, _ := newTestServer(t, "test-client", &stubSvcAcctDB{}, nil)
	_, err := s.AuthMachine(context.Background(), &authv1.AuthMachineRequest{ClientId: "some-id"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestAuthMachine_UnknownClientID_Unauthenticated(t *testing.T) {
	s, _ := newTestServer(t, "test-client", &stubSvcAcctDB{}, nil)
	_, err := s.AuthMachine(context.Background(), &authv1.AuthMachineRequest{
		ClientId:     "00000000-0000-0000-0000-000000000000",
		ClientSecret: "secret",
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}

func TestAuthMachine_WrongSecret_Unauthenticated(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	saDB := &stubSvcAcctDB{row: &db.ServiceAccountRow{
		ID:               "sa-uuid-1",
		Name:             "test-sa",
		ClientSecretHash: string(hash),
		Role:             "service_account",
	}}
	s, _ := newTestServer(t, "test-client", saDB, nil)
	_, err = s.AuthMachine(context.Background(), &authv1.AuthMachineRequest{
		ClientId:     "sa-uuid-1",
		ClientSecret: "wrong-secret",
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}

func TestAuthMachine_ValidCredentials_ReturnsSessionToken(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	saDB := &stubSvcAcctDB{row: &db.ServiceAccountRow{
		ID:               "sa-uuid-1",
		Name:             "test-sa",
		ClientSecretHash: string(hash),
		Role:             "service_account",
	}}
	s, sessStore := newTestServer(t, "test-client", saDB, nil)
	resp, err := s.AuthMachine(context.Background(), &authv1.AuthMachineRequest{
		ClientId:     "sa-uuid-1",
		ClientSecret: "correct-secret",
	})
	if err != nil {
		t.Fatalf("AuthMachine: %v", err)
	}
	if resp.Session == nil || resp.Session.SessionId == "" {
		t.Fatal("expected non-empty session_id")
	}
	if resp.Session.ExpiresAt == nil {
		t.Fatal("expected non-nil expires_at")
	}
	// Verify the session TTL matches machineSessionTTL (1h)
	data := sessStore.sessions[resp.Session.SessionId]
	if data == nil {
		t.Fatal("session not found in store")
	}
	ttl := data.ExpiresAt.Sub(data.CreatedAt)
	if ttl != time.Hour {
		t.Fatalf("expected session TTL=1h, got %v", ttl)
	}
	if data.Kind != session.SessionKindServiceAccount {
		t.Fatalf("expected session kind service_account, got %q", data.Kind)
	}
}
