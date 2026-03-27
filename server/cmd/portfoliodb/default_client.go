//go:build !e2e

package main

import (
	"net/http"
	"time"

	"google.golang.org/grpc"
)

// newPluginHTTPClient returns the shared HTTP client for plugins.
// Timeout is a safety net only; effective per-plugin timeouts are controlled
// via context deadlines (timeout_seconds in plugin_config.config JSONB).
func newPluginHTTPClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Minute}
}

func newDescriptionHTTPClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second}
}

func stopE2ERecorder() {}

func registerE2EService(_ *grpc.Server) {}

func e2eSkipPrefixes() []string { return nil }
