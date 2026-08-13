package auth

import (
	"context"
	"net/http"
	"testing"

	"google.golang.org/grpc/metadata"
)

// What the session id is read out of, which is the one piece of an authenticated
// request that an attacker writes in full.
//
// The parsing behind SessionIDFromContext is hand-rolled -- readCookies, and with it
// splitCookie, indexByte and trimSpace, which are strings.Split, strings.IndexByte
// and a partial strings.TrimSpace. Nothing exercised any of it. These tests go
// through the exported entry point rather than at the four helpers, because that is
// what the interceptor calls and the helpers are an implementation of it.
//
// Where the hand-rolled parser and net/http disagree is recorded below, in
// TestSessionIDFromContext_DivergesFromNetHTTP. Those cases are written as
// what the code does today rather than what it ought to do, so that replacing the
// parser shows up as a diff in the expectations rather than as silence.

// ctxWithMetadata returns a context carrying the given incoming metadata pairs.
func ctxWithMetadata(kv ...string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(kv...))
}

func TestSessionIDFromContext(t *testing.T) {
	const name = "portfoliodb_session"
	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{
			name: "the cookie on its own",
			ctx:  ctxWithMetadata("cookie", name+"=abc123"),
			want: "abc123",
		},
		{
			name: "picked out from among others",
			ctx:  ctxWithMetadata("cookie", "theme=dark; "+name+"=abc123; lang=en"),
			want: "abc123",
		},
		{
			name: "a cookie of another name is not it",
			ctx:  ctxWithMetadata("cookie", "other=abc123"),
			want: "",
		},
		{
			// The name is compared exactly, so a differently-cased cookie is a
			// different cookie rather than the same one.
			name: "the name is case sensitive",
			ctx:  ctxWithMetadata("cookie", "PORTFOLIODB_SESSION=abc123"),
			want: "",
		},
		{
			name: "surrounding whitespace is not part of it",
			ctx:  ctxWithMetadata("cookie", "  "+name+"  =  abc123  "),
			want: "abc123",
		},
		{
			name: "a value holding an equals sign keeps it",
			ctx:  ctxWithMetadata("cookie", name+"=a=b=c"),
			want: "a=b=c",
		},
		{
			// The first wins. Nothing in the format says which of two cookies of
			// one name is meant, and taking the later one would let an attacker
			// who can append a cookie displace the real session.
			name: "the first of two of the same name wins",
			ctx:  ctxWithMetadata("cookie", name+"=first; "+name+"=second"),
			want: "first",
		},
		{
			name: "an empty value is empty rather than absent",
			ctx:  ctxWithMetadata("cookie", name+"="),
			want: "",
		},
		{
			name: "a bare name with no value is skipped",
			ctx:  ctxWithMetadata("cookie", name),
			want: "",
		},
		{
			name: "stray semicolons are not cookies",
			ctx:  ctxWithMetadata("cookie", ";;"+name+"=abc123;;"),
			want: "abc123",
		},
		{
			name: "an empty header yields nothing",
			ctx:  ctxWithMetadata("cookie", ""),
			want: "",
		},
		{
			// Two Cookie headers rather than one, which is what a client sending
			// them separately produces.
			name: "a second cookie header is read too",
			ctx: metadata.NewIncomingContext(context.Background(), metadata.MD{
				"cookie": []string{"theme=dark", name + "=abc123"},
			}),
			want: "abc123",
		},
		{
			// The machine path: a service account holds a bearer token rather than
			// a cookie, and the same lookup answers for both.
			name: "a bearer token stands in for the cookie",
			ctx:  ctxWithMetadata("authorization", "Bearer machine-token"),
			want: "machine-token",
		},
		{
			name: "the cookie is preferred over a bearer token",
			ctx: metadata.NewIncomingContext(context.Background(), metadata.MD{
				"cookie":        []string{name + "=abc123"},
				"authorization": []string{"Bearer machine-token"},
			}),
			want: "abc123",
		},
		{
			// Only the Bearer scheme. Basic credentials are not a session id, and
			// treating the whole header as one would look up whatever was there.
			name: "another authorization scheme is not a session",
			ctx:  ctxWithMetadata("authorization", "Basic dXNlcjpwYXNz"),
			want: "",
		},
		{
			name: "no metadata at all yields nothing",
			ctx:  context.Background(),
			want: "",
		},
		{
			name: "metadata with neither header yields nothing",
			ctx:  ctxWithMetadata("user-agent", "curl"),
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SessionIDFromContext(tt.ctx, name); got != tt.want {
				t.Errorf("session id: got %q, want %q", got, tt.want)
			}
		})
	}
}

// Where the hand-rolled parser and net/http disagree about the same header.
//
// A value reaching here goes on to be a Redis key and to be echoed back in a
// GetSession response. Neither can be injected into by a value of any shape -- the
// Redis protocol is length-prefixed and the response is protobuf -- so none of these
// is a hole today. What they are is a wider set of accepted values than any browser
// will ever send, on the one input an attacker writes in full.
//
// net/http is already imported by this file's subject, and its own parser is what
// readCookies, splitCookie, indexByte and trimSpace are a partial reimplementation
// of. The stdlib column is what (&http.Request{Header: ...}).Cookies() gives for the
// same header, so it is both the record of the difference and the check that the
// difference is still there. Every case in the test above agrees between the two;
// these are the whole of the disagreement.
func TestSessionIDFromContext_DivergesFromNetHTTP(t *testing.T) {
	const name = "portfoliodb_session"
	tests := []struct {
		name   string
		value  string
		want   string // what readCookies yields
		stdlib string // what net/http yields for the same header
	}{
		{name: "a carriage return", value: "a\rb", want: "a\rb", stdlib: ""},
		{name: "a newline", value: "a\nb", want: "a\nb", stdlib: ""},
		{name: "a NUL", value: "a\x00b", want: "a\x00b", stdlib: ""},
		{name: "a backslash", value: `a\b`, want: `a\b`, stdlib: ""},
		{name: "a DEL", value: "\x7f", want: "\x7f", stdlib: ""},
		{
			// A tab inside the value survives; one at either end is trimmed off,
			// which is trimSpace rather than anything the format asks for.
			name: "an interior tab", value: "a\tb", want: "a\tb", stdlib: "",
		},
		{
			// The one difference that is not about accepting more: net/http reads
			// the quotes as delimiters and this reads them as part of the value, so
			// a quoted session id here matches no stored session.
			name: "surrounding quotes", value: `"abc"`, want: `"abc"`, stdlib: "abc",
		},
		{
			// trimSpace takes the space off; net/http keeps it, so the two look up
			// different sessions for one header.
			name: "a leading space", value: " abc", want: "abc", stdlib: " abc",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := name + "=" + tt.value
			if got := SessionIDFromContext(ctxWithMetadata("cookie", header), name); got != tt.want {
				t.Errorf("session id: got %q, want %q", got, tt.want)
			}
			std := ""
			req := &http.Request{Header: http.Header{"Cookie": []string{header}}}
			for _, c := range req.Cookies() {
				if c.Name == name {
					std = c.Value
					break
				}
			}
			if std != tt.stdlib {
				t.Errorf("net/http now yields %q for %q, not %q; this test is out of date", std, header, tt.stdlib)
			}
		})
	}
}
