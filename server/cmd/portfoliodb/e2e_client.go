//go:build e2e

package main

import (
	"log"
	"net/http"
	"time"
)

func newPluginHTTPClient() *http.Client {
	return &http.Client{
		Transport: e2eTransport,
		Timeout:   30 * time.Second,
	}
}

func newDescriptionHTTPClient() *http.Client {
	return &http.Client{
		Transport: e2eTransport,
		Timeout:   20 * time.Second,
	}
}

// stopE2ERecorder flushes any active cassette. Called during graceful shutdown
// as a safety net (normally cassettes are flushed by UnloadCassette RPCs).
func stopE2ERecorder() {
	e2eMu.Lock()
	defer e2eMu.Unlock()
	if e2eRec != nil {
		if err := e2eRec.Stop(); err != nil {
			log.Printf("e2e vcr: stop error: %v", err)
		} else {
			log.Printf("e2e vcr: recorder stopped on shutdown")
		}
		e2eRec = nil
	}
	e2eTransport.swap(nil)
}
