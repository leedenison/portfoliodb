//go:build !e2e

package main

import (
	"net/http"
	"time"

	"google.golang.org/grpc"
)

func newPluginHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

func newDescriptionHTTPClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second}
}

func stopE2ERecorder() {}

func registerE2EService(_ *grpc.Server) {}

func e2eSkipPrefixes() []string { return nil }
