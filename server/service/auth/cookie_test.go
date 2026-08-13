package auth

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

// What the session id is read out of, which is the one piece of an authenticated
// request that an attacker writes in full.
//
// The parsing is net/http's. It was hand-rolled -- readCookies, and with it
// splitCookie, indexByte and trimSpace, which were strings.Split, strings.IndexByte
// and a partial strings.TrimSpace -- and none of it was exercised. The tests below
// go through the exported entry point rather than at whatever is under it, because
// that is what the interceptor calls.

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
			// Space around the name is not part of the name, which is what lets the
			// usual "a=1; b=2" spacing work at all. Space after the equals is part
			// of the value, because nothing says it is not and trimming it would
			// make two different headers name one session.
			name: "space around the name is not part of it, space in the value is",
			ctx:  ctxWithMetadata("cookie", "  "+name+"  =  abc123"),
			want: "  abc123",
		},
		{
			name: "a value holding an equals sign keeps it",
			ctx:  ctxWithMetadata("cookie", name+"=a=b=c"),
			want: "a=b=c",
		},
		{
			// A semicolon is the separator, so it ends the value rather than being
			// part of it. A session id cannot contain one, so nothing is lost.
			name: "a semicolon ends the value",
			ctx:  ctxWithMetadata("cookie", name+"=abc;123"),
			want: "abc",
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
			// Quotes delimit the value rather than being part of it, so this names
			// the same session as the unquoted form.
			name: "a quoted value is unwrapped",
			ctx:  ctxWithMetadata("cookie", name+`="abc123"`),
			want: "abc123",
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

// A value no browser will ever have sent names no session.
//
// Each of these was accepted verbatim by the parser this replaced, and each is now
// dropped. None of them was a hole -- the value goes on to be a Redis key, whose
// protocol is length-prefixed, and to be echoed into a protobuf response, so there
// was nothing to inject into. What they are is the difference between accepting
// what the format permits and accepting whatever arrived, on the one input an
// attacker writes in full.
//
// A dropped cookie falls through to the bearer token and then to nothing, so the
// request is unauthenticated rather than authenticated as somebody. That is the
// property here: none of these returns a session id.
func TestSessionIDFromContext_RejectsWhatNoBrowserSends(t *testing.T) {
	const name = "portfoliodb_session"
	tests := []struct {
		name  string
		value string
	}{
		{name: "a carriage return", value: "a\rb"},
		{name: "a newline", value: "a\nb"},
		{name: "a NUL", value: "a\x00b"},
		{name: "a backslash", value: `a\b`},
		{name: "a DEL", value: "\x7f"},
		{name: "an interior tab", value: "a\tb"},
		{name: "an interior quote", value: `a"b`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := ctxWithMetadata("cookie", name+"="+tt.value)
			if got := SessionIDFromContext(ctx, name); got != "" {
				t.Errorf("session id: got %q, want nothing for a value the format does not permit", got)
			}
		})
	}
}

// One bad cookie does not take the session down with it.
//
// This is why the parsing goes through a request rather than through
// http.ParseCookie, which rejects the whole header when any part of it is
// malformed: a cookie set by something else entirely would then log the user out.
func TestSessionIDFromContext_SurvivesAMalformedNeighbour(t *testing.T) {
	const name = "portfoliodb_session"
	tests := []struct {
		name   string
		header string
	}{
		{"a neighbour with a control character", "junk=a\x00b; " + name + "=abc123"},
		{"a neighbour with no value", "junk; " + name + "=abc123"},
		{"a neighbour with no name", "=junk; " + name + "=abc123"},
		{"a trailing semicolon", name + "=abc123; "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SessionIDFromContext(ctxWithMetadata("cookie", tt.header), name); got != "abc123" {
				t.Errorf("session id: got %q, want abc123", got)
			}
		})
	}
}
