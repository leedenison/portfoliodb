package google

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	googleJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"
	// DefaultJWKSCacheTTL is the default time to cache Google's JWKS.
	DefaultJWKSCacheTTL = 1 * time.Hour
	// DefaultClockSkew allows 2 minutes clock skew for exp/iat.
	DefaultClockSkew = 2 * time.Minute
)

// Claims holds the Google ID token claims we care about.
type Claims struct {
	jwt.RegisteredClaims
	Email         string `json:"email"`
	EmailVerified *bool  `json:"email_verified"`
	Name          string `json:"name"`
}

// Verifier verifies Google ID tokens using cached JWKS.
type Verifier struct {
	clientID     string
	clockSkew    time.Duration
	jwksURL      string
	cacheTTL     time.Duration
	httpClient   *http.Client
	mu           sync.RWMutex
	cachedKeys   map[string]*rsa.PublicKey
	cacheExpiry  time.Time
}

// VerifierOption configures a Verifier.
type VerifierOption func(*Verifier)

// WithJWKSCacheTTL sets the JWKS cache TTL.
func WithJWKSCacheTTL(d time.Duration) VerifierOption {
	return func(v *Verifier) {
		v.cacheTTL = d
	}
}

// WithClockSkew sets the allowed clock skew for exp/iat.
func WithClockSkew(d time.Duration) VerifierOption {
	return func(v *Verifier) {
		v.clockSkew = d
	}
}

// WithHTTPClient sets the HTTP client for fetching JWKS.
func WithHTTPClient(c *http.Client) VerifierOption {
	return func(v *Verifier) {
		v.httpClient = c
	}
}

// NewVerifier returns a Verifier for the given Google OAuth client ID.
func NewVerifier(clientID string, opts ...VerifierOption) *Verifier {
	v := &Verifier{
		clientID:   clientID,
		clockSkew:  DefaultClockSkew,
		jwksURL:    googleJWKSURL,
		cacheTTL:   DefaultJWKSCacheTTL,
		httpClient: http.DefaultClient,
		cachedKeys: make(map[string]*rsa.PublicKey),
	}
	for _, o := range opts {
		o(v)
	}
	return v
}

// VerifyResult holds the extracted identity from a valid token.
type VerifyResult struct {
	Sub   string // Google stable user ID
	Email string
	Name  string // optional; not always in ID token
}

// Verify verifies the Google ID token and returns the claims, or an error.
// Returns: Unauthenticated for invalid/missing token or wrong aud/iss;
// PermissionDenied for unverified email; InvalidArgument for missing token.
func (v *Verifier) Verify(ctx context.Context, idToken string) (*VerifyResult, error) {
	if idToken == "" {
		return nil, fmt.Errorf("%w: missing google_id_token", ErrInvalidArgument)
	}
	// Parse without verification to get kid and fetch the right key.
	tok, err := jwt.ParseWithClaims(idToken, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		kid, ok := t.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("missing kid in header")
		}
		key, err := v.getKey(ctx, kid)
		if err != nil {
			return nil, err
		}
		return key, nil
	}, jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(v.clockSkew))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnauthenticated, err)
	}
	claims, ok := tok.Claims.(*Claims)
	if !ok || !tok.Valid {
		return nil, fmt.Errorf("%w: invalid token", ErrUnauthenticated)
	}
	// Issuer: https://accounts.google.com or accounts.google.com
	if claims.Issuer != "https://accounts.google.com" && claims.Issuer != "accounts.google.com" {
		return nil, fmt.Errorf("%w: wrong issuer", ErrUnauthenticated)
	}
	// Audience must match client ID
	audMatch := false
	for _, a := range claims.Audience {
		if a == v.clientID {
			audMatch = true
			break
		}
	}
	if !audMatch {
		return nil, fmt.Errorf("%w: wrong audience", ErrUnauthenticated)
	}
	// Email verified required
	if claims.EmailVerified != nil && !*claims.EmailVerified {
		return nil, fmt.Errorf("%w: email not verified", ErrPermissionDenied)
	}
	if claims.Email == "" {
		return nil, fmt.Errorf("%w: missing email", ErrPermissionDenied)
	}
	return &VerifyResult{
		Sub:   claims.Subject,
		Email: claims.Email,
		Name:  claims.Name,
	}, nil
}

func (v *Verifier) getKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.RLock()
	if key, ok := v.cachedKeys[kid]; ok && time.Now().Before(v.cacheExpiry) {
		v.mu.RUnlock()
		return key, nil
	}
	v.mu.RUnlock()

	v.mu.Lock()
	defer v.mu.Unlock()
	// Double-check after acquiring write lock
	if key, ok := v.cachedKeys[kid]; ok && time.Now().Before(v.cacheExpiry) {
		return key, nil
	}
	// Refresh cache
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks fetch: %s", resp.Status)
	}
	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, err
	}
	v.cachedKeys = make(map[string]*rsa.PublicKey)
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pub, err := jwkToRSAPublic(k.N, k.E)
		if err != nil {
			continue
		}
		v.cachedKeys[k.Kid] = pub
	}
	v.cacheExpiry = time.Now().Add(v.cacheTTL)

	if key, ok := v.cachedKeys[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("key %q not found in JWKS", kid)
}

func jwkToRSAPublic(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, err
	}
	n := new(big.Int).SetBytes(nBytes)
	var e int
	for _, b := range eBytes {
		e = e<<8 + int(b)
	}
	if e == 0 {
		e = 65537
	}
	return &rsa.PublicKey{N: n, E: e}, nil
}
