// Package vcr provides helpers for go-vcr based integration tests.
// It configures a recorder that records HTTP interactions in record mode
// (VCR_MODE=record) and replays them from cassettes otherwise.
package vcr

import (
	"net/http"
	"net/url"
	"os"
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
	for _, h := range []string{"Openai-Organization", "Openai-Project", "Set-Cookie", "Cf-Ray"} {
		delete(i.Response.Headers, h)
	}

	return nil
}

// IsRecording returns true when VCR_MODE is set to "record".
func IsRecording() bool {
	return os.Getenv("VCR_MODE") == "record"
}

// New creates a go-vcr recorder and an *http.Client that uses its transport.
// In record mode (VCR_MODE=record) real HTTP requests are made and saved to
// cassettePath. In replay mode (default) responses are played back from the
// cassette file. The sanitizer, if non-nil, strips secrets before saving.
// The recorder is stopped automatically when the test completes.
func New(t *testing.T, cassettePath string, sanitize Sanitizer) (*recorder.Recorder, *http.Client) {
	t.Helper()

	mode := recorder.ModeReplayOnly
	if IsRecording() {
		mode = recorder.ModeRecordOnly
	}

	opts := []recorder.Option{
		recorder.WithMode(mode),
		recorder.WithSkipRequestLatency(true),
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

// EnvOrSkip returns the value of the named environment variable. In record
// mode it fails the test if the variable is empty. In replay mode it returns
// "REDACTED" as a placeholder since cassettes contain no real credentials.
func EnvOrSkip(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if IsRecording() {
		if v == "" {
			t.Fatalf("env var %s required in record mode", name)
		}
		return v
	}
	return "REDACTED"
}
