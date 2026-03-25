//go:build e2e

package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
)

// debugTransport wraps an http.RoundTripper and logs every request.
type debugTransport struct {
	name    string
	wrapped http.RoundTripper
}

func (d *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	log.Printf("e2e vcr DEBUG: %s RoundTrip called: %s %s (transport type: %T)", d.name, req.Method, req.URL, d.wrapped)
	return d.wrapped.RoundTrip(req)
}

// e2eRecorder is the shared VCR recorder for all plugin HTTP clients.
// It is stopped via stopE2ERecorder during graceful shutdown.
var e2eRecorder *recorder.Recorder

func init() {
	cassetteDir := os.Getenv("E2E_CASSETTE_DIR")
	if cassetteDir == "" {
		cassetteDir = "e2e/cassettes"
	}
	cassettePath := cassetteDir + "/plugins"

	mode := recorder.ModeReplayOnly
	if os.Getenv("E2E_VCR_MODE") == "record" {
		mode = recorder.ModeRecordOnly
	}

	opts := []recorder.Option{
		recorder.WithMode(mode),
		recorder.WithSkipRequestLatency(true),
		recorder.WithHook(sanitizeE2E, recorder.BeforeSaveHook),
	}

	var err error
	e2eRecorder, err = recorder.New(cassettePath, opts...)
	if err != nil {
		log.Fatalf("e2e vcr recorder: %v", err)
	}
	log.Printf("e2e vcr: E2E_VCR_MODE=%q mode=%v cassette=%s", os.Getenv("E2E_VCR_MODE"), mode, cassettePath)
}

// stopE2ERecorder flushes recorded cassettes to disk (record mode)
// or releases resources (replay mode). Called during graceful shutdown.
func stopE2ERecorder() {
	if e2eRecorder != nil {
		if err := e2eRecorder.Stop(); err != nil {
			log.Printf("e2e vcr: stop error: %v", err)
		} else {
			log.Printf("e2e vcr: recorder stopped, cassette flushed")
		}
	}
}

func newPluginHTTPClient() *http.Client {
	transport := e2eRecorder.GetDefaultClient().Transport
	log.Printf("e2e vcr DEBUG: newPluginHTTPClient transport type=%T, is recorder=%v, recorder mode=%v", transport, fmt.Sprintf("%p", transport) == fmt.Sprintf("%p", e2eRecorder), e2eRecorder.Mode())
	return &http.Client{
		Transport: &debugTransport{name: "plugin", wrapped: transport},
		Timeout:   30 * time.Second,
	}
}

func newDescriptionHTTPClient() *http.Client {
	transport := e2eRecorder.GetDefaultClient().Transport
	log.Printf("e2e vcr DEBUG: newDescriptionHTTPClient transport type=%T, is recorder=%v, recorder mode=%v", transport, fmt.Sprintf("%p", transport) == fmt.Sprintf("%p", e2eRecorder), e2eRecorder.Mode())
	return &http.Client{
		Transport: &debugTransport{name: "description", wrapped: transport},
		Timeout:   20 * time.Second,
	}
}

// sanitizeE2E redacts API keys from recorded cassettes.
func sanitizeE2E(i *cassette.Interaction) error {
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

	// Massive / EODHD: API key in query parameter.
	u, err := url.Parse(i.Request.URL)
	if err == nil {
		q := u.Query()
		changed := false
		for _, param := range []string{"api_token", "api_key"} {
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

	return nil
}
