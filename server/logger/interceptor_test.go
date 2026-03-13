package logger

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func testLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestUnaryErrorInterceptor(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantLog   bool
		wantSubstr string
	}{
		{name: "nil error", err: nil, wantLog: false},
		{name: "invalid argument", err: status.Error(codes.InvalidArgument, "bad input"), wantLog: false},
		{name: "not found", err: status.Error(codes.NotFound, "missing"), wantLog: false},
		{name: "unauthenticated", err: status.Error(codes.Unauthenticated, "no session"), wantLog: false},
		{name: "permission denied", err: status.Error(codes.PermissionDenied, "forbidden"), wantLog: false},
		{name: "internal", err: status.Error(codes.Internal, "db down"), wantLog: true, wantSubstr: "db down"},
		{name: "unknown", err: status.Error(codes.Unknown, "unexpected"), wantLog: true, wantSubstr: "unexpected"},
		{name: "unavailable", err: status.Error(codes.Unavailable, "overloaded"), wantLog: true, wantSubstr: "overloaded"},
		{name: "data loss", err: status.Error(codes.DataLoss, "corrupt"), wantLog: true, wantSubstr: "corrupt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := testLogger(&buf)
			interceptor := UnaryErrorInterceptor(log)

			handler := func(ctx context.Context, req interface{}) (interface{}, error) {
				return "ok", tc.err
			}
			info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

			resp, err := interceptor(context.Background(), nil, info, handler)
			if err != tc.err {
				t.Fatalf("got err %v, want %v", err, tc.err)
			}
			if tc.err == nil && resp != "ok" {
				t.Fatalf("got resp %v, want ok", resp)
			}

			logged := buf.String()
			if tc.wantLog && logged == "" {
				t.Fatal("expected log output, got none")
			}
			if !tc.wantLog && logged != "" {
				t.Fatalf("expected no log output, got: %s", logged)
			}
			if tc.wantSubstr != "" && !bytes.Contains(buf.Bytes(), []byte(tc.wantSubstr)) {
				t.Fatalf("log output missing %q: %s", tc.wantSubstr, logged)
			}
			if tc.wantLog && !bytes.Contains(buf.Bytes(), []byte("/test.Service/Method")) {
				t.Fatalf("log output missing method: %s", logged)
			}
		})
	}
}

type fakeServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f *fakeServerStream) Context() context.Context { return f.ctx }

func TestStreamErrorInterceptor(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantLog bool
	}{
		{name: "nil error", err: nil, wantLog: false},
		{name: "invalid argument", err: status.Error(codes.InvalidArgument, "bad"), wantLog: false},
		{name: "internal", err: status.Error(codes.Internal, "broken"), wantLog: true},
		{name: "unavailable", err: status.Error(codes.Unavailable, "busy"), wantLog: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := testLogger(&buf)
			interceptor := StreamErrorInterceptor(log)

			handler := func(srv interface{}, stream grpc.ServerStream) error {
				return tc.err
			}
			info := &grpc.StreamServerInfo{FullMethod: "/test.Service/Stream"}
			ss := &fakeServerStream{ctx: context.Background()}

			err := interceptor(nil, ss, info, handler)
			if err != tc.err {
				t.Fatalf("got err %v, want %v", err, tc.err)
			}

			logged := buf.String()
			if tc.wantLog && logged == "" {
				t.Fatal("expected log output, got none")
			}
			if !tc.wantLog && logged != "" {
				t.Fatalf("expected no log output, got: %s", logged)
			}
		})
	}
}

func TestIs5xx(t *testing.T) {
	tests := []struct {
		name string
		code codes.Code
		want bool
	}{
		{name: "OK", code: codes.OK, want: false},
		{name: "Canceled", code: codes.Canceled, want: false},
		{name: "InvalidArgument", code: codes.InvalidArgument, want: false},
		{name: "NotFound", code: codes.NotFound, want: false},
		{name: "AlreadyExists", code: codes.AlreadyExists, want: false},
		{name: "PermissionDenied", code: codes.PermissionDenied, want: false},
		{name: "Unauthenticated", code: codes.Unauthenticated, want: false},
		{name: "ResourceExhausted", code: codes.ResourceExhausted, want: false},
		{name: "FailedPrecondition", code: codes.FailedPrecondition, want: false},
		{name: "Aborted", code: codes.Aborted, want: false},
		{name: "OutOfRange", code: codes.OutOfRange, want: false},
		{name: "Unimplemented", code: codes.Unimplemented, want: false},
		{name: "DeadlineExceeded", code: codes.DeadlineExceeded, want: false},
		{name: "Unknown", code: codes.Unknown, want: true},
		{name: "Internal", code: codes.Internal, want: true},
		{name: "Unavailable", code: codes.Unavailable, want: true},
		{name: "DataLoss", code: codes.DataLoss, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := is5xx(tc.code); got != tc.want {
				t.Fatalf("is5xx(%v) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}
