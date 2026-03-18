// Package vcr provides helpers for go-vcr based integration tests.
// It configures a recorder that records HTTP interactions in record mode
// (VCR_MODE=record) and replays them from cassettes otherwise.
package vcr

import (
	"net/http"
	"os"
	"testing"

	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
)

// Sanitizer modifies a recorded interaction to remove secrets before saving.
type Sanitizer func(i *cassette.Interaction) error

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
