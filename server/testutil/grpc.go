package testutil

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RequireGRPCCode fails the test if err is nil or if status.Code(err) != want.
func RequireGRPCCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	if got := status.Code(err); got != want {
		t.Fatalf("status.Code(err) = %v, want %v", got, want)
	}
}
