//go:build e2e

package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/leedenison/portfoliodb/server/testutil/vcr"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
)

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
	if os.Getenv("VCR_MODE") == "record" {
		mode = recorder.ModeRecordOnly
	}

	opts := []recorder.Option{
		recorder.WithMode(mode),
		recorder.WithSkipRequestLatency(true),
		recorder.WithHook(vcr.SanitizeAll, recorder.BeforeSaveHook),
	}

	var err error
	e2eRecorder, err = recorder.New(cassettePath, opts...)
	if err != nil {
		log.Fatalf("e2e vcr recorder: %v", err)
	}
	log.Printf("e2e vcr: mode=%v cassette=%s", mode, cassettePath)
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
	return &http.Client{
		Transport: e2eRecorder.GetDefaultClient().Transport,
		Timeout:   30 * time.Second,
	}
}

func newDescriptionHTTPClient() *http.Client {
	return &http.Client{
		Transport: e2eRecorder.GetDefaultClient().Transport,
		Timeout:   20 * time.Second,
	}
}
