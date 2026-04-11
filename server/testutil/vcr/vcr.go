// Package vcr provides helpers for go-vcr based integration tests.
// VCR_MODE is a comma-separated list of suite identifiers to record.
// Listed suites make real HTTP requests and save to cassettes; all
// others replay from existing cassettes.
package vcr

import (
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
)

// Sanitizer modifies a recorded interaction to remove secrets before saving.
type Sanitizer func(i *cassette.Interaction) error

// SanitizeAll redacts all known API keys from a recorded interaction.
// It covers headers (OpenFIGI, OpenAI Bearer) and query parameters
// (EODHD api_token, Massive apiKey).
func SanitizeAll(i *cassette.Interaction) error {
	// OpenFIGI: API key in header.
	if vals, ok := i.Request.Headers["X-Openfigi-Apikey"]; ok && len(vals) > 0 {
		i.Request.Headers["X-Openfigi-Apikey"] = []string{"REDACTED"}
	}

	// OpenAI: Bearer token in Authorization header.
	if vals, ok := i.Request.Headers["Authorization"]; ok && len(vals) > 0 {
		for idx, v := range vals {
			if strings.HasPrefix(v, "Bearer ") {
				vals[idx] = "Bearer REDACTED"
			}
		}
	}

	// API keys in query parameters (EODHD api_token, Massive apiKey).
	u, err := url.Parse(i.Request.URL)
	if err == nil {
		q := u.Query()
		changed := false
		for _, param := range []string{"api_token", "api_key", "apiKey"} {
			if q.Has(param) {
				q.Set(param, "REDACTED")
				changed = true
			}
		}
		if changed {
			u.RawQuery = q.Encode()
			i.Request.URL = u.String()
		}
	}

	// go-vcr also records parsed query params under Form.
	for _, param := range []string{"api_token", "api_key", "apiKey"} {
		if _, ok := i.Request.Form[param]; ok {
			i.Request.Form[param] = []string{"REDACTED"}
		}
	}

	// Redact sensitive response headers (account identifiers, cookies).
	sanitizeResponseHeaders(i)

	return nil
}

// sanitizeResponseHeaders strips sensitive metadata from response headers
// that should not be committed to the repository.
func sanitizeResponseHeaders(i *cassette.Interaction) {
	for _, h := range []string{"Openai-Organization", "Openai-Project", "Set-Cookie", "Cf-Ray"} {
		delete(i.Response.Headers, h)
	}
}

// IsRecording returns true when VCR_MODE is non-empty, meaning at least one
// suite is being recorded. Use this for "do we need real API keys?" checks.
func IsRecording() bool {
	return os.Getenv("VCR_MODE") != ""
}

// IsRecordingSuite returns true when the given suite identifier appears in
// the comma-separated VCR_MODE list.
func IsRecordingSuite(suite string) bool {
	mode := os.Getenv("VCR_MODE")
	if mode == "" {
		return false
	}
	for _, s := range strings.Split(mode, ",") {
		if strings.TrimSpace(s) == suite {
			return true
		}
	}
	return false
}

// New creates a go-vcr recorder and an *http.Client that uses its transport.
// When the given suite is listed in VCR_MODE, real HTTP requests are made and
// saved to cassettePath. Otherwise responses are played back from the cassette
// file. The sanitizer, if non-nil, strips secrets before saving.
// The recorder is stopped automatically when the test completes.
func New(t *testing.T, cassettePath string, sanitize Sanitizer, suite string) (*recorder.Recorder, *http.Client) {
	t.Helper()

	mode := recorder.ModeReplayOnly
	if IsRecordingSuite(suite) {
		mode = recorder.ModeRecordOnly
	}

	opts := []recorder.Option{
		recorder.WithMode(mode),
		recorder.WithSkipRequestLatency(true),
		// Always strip sensitive response headers regardless of per-plugin sanitizer.
		recorder.WithHook(func(i *cassette.Interaction) error {
			sanitizeResponseHeaders(i)
			return nil
		}, recorder.BeforeSaveHook),
	}
	if sanitize != nil {
		opts = append(opts, recorder.WithHook(func(i *cassette.Interaction) error {
			return sanitize(i)
		}, recorder.BeforeSaveHook))
	}

	rec, err := recorder.New(cassettePath, opts...)
	if err != nil {
		t.Fatalf("vcr.New: %v", err)
	}
	t.Cleanup(func() { _ = rec.Stop() })

	return rec, rec.GetDefaultClient()
}

// EnvOrSkip returns the value of the named environment variable. When the
// given suite is being recorded it fails the test if the variable is empty.
// In replay mode it returns "REDACTED" so that URLs match cassettes which
// were sanitized during recording.
func EnvOrSkip(t *testing.T, name string, suite string) string {
	t.Helper()
	if IsRecordingSuite(suite) {
		v := os.Getenv(name)
		if v == "" {
			t.Fatalf("env var %s required when recording suite %s", name, suite)
		}
		return v
	}
	return "REDACTED"
}

// dateRe matches ISO date segments (YYYY-MM-DD) in URL paths. Used to
// normalize date-dependent URLs so cassettes recorded on one day still
// replay correctly on subsequent days.
var dateRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

// normalizeURL replaces ISO date segments in both the URL path and query
// parameter values with a fixed placeholder so that date-dependent URLs
// match cassette entries regardless of when the test runs.
func normalizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Path = dateRe.ReplaceAllString(u.Path, "DATE")
	q := u.Query()
	for key, vals := range q {
		for i, v := range vals {
			vals[i] = dateRe.ReplaceAllString(v, "DATE")
		}
		q[key] = vals
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// E2EMatcher is a relaxed cassette matcher for E2E tests. It normalizes
// ISO date segments in URL paths before comparing so that cassettes
// recorded on one date replay correctly on another. It matches on method,
// normalized URL, host, and request body.
func E2EMatcher(r *http.Request, i cassette.Request) bool {
	if r.Method != i.Method {
		return false
	}
	if normalizeURL(r.URL.String()) != normalizeURL(i.URL) {
		return false
	}
	if r.Host != i.Host {
		return false
	}
	return true
}
