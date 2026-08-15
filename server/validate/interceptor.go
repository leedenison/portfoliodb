// Package validate applies the protovalidate rules a request's proto carries,
// and keeps what it reports about a bad request small enough to travel.
package validate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"buf.build/go/protovalidate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// How much of a validation failure an InvalidArgument states.
//
// A gRPC status message travels in the response HEADERS frame, and a proxy
// rejects headers over its own limit rather than forwarding them: Envoy's
// default is 60 KiB, and what it does on a larger response is reset the stream,
// so the caller sees a transport failure instead of the error. That turns the
// clearest answer the server has -- your file is wrong, here is where -- into an
// opaque 503, and it happens exactly when the answer is most needed.
//
// A per-element rule makes the message proportional to the upload rather than to
// the mistake: an archive of a thousand postings that each miss a required field
// produces a thousand violations, and there is no size of file for which the
// thousandth is the one that explains it. So the message names the first few and
// counts the rest.
//
// The byte cap is the backstop for one enormous violation -- a CEL message
// quoting a long field value -- and is far enough under the header budget to
// survive grpc-message's percent-encoding, which can treble a non-ASCII byte.
const (
	MaxViolations   = 20
	MaxMessageBytes = 4 << 10
)

// UnaryInterceptor rejects a request that violates the rules its proto carries,
// before the handler sees it.
func UnaryInterceptor(v protovalidate.Validator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if msg, ok := req.(proto.Message); ok {
			if err := v.Validate(msg); err != nil {
				return nil, status.Error(codes.InvalidArgument, Message(err))
			}
		}
		return handler(ctx, req)
	}
}

// Message is what a validation failure says on the wire: the same text
// protovalidate writes, capped at MaxViolations violations and MaxMessageBytes
// bytes.
//
// An error that is not a ValidationError -- a compilation or runtime failure --
// is passed through under the same byte cap, since it is bounded by the schema
// rather than by the request but still ends up in the same header.
func Message(err error) string {
	var invalid *protovalidate.ValidationError
	if !errors.As(err, &invalid) {
		return truncate(err.Error())
	}
	shown := invalid.Violations
	if len(shown) > MaxViolations {
		shown = shown[:MaxViolations]
	}
	if len(invalid.Violations) == 1 {
		return truncate("validation error: " + shown[0].String())
	}
	b := &strings.Builder{}
	b.WriteString("validation errors:")
	for _, violation := range shown {
		b.WriteString("\n - ")
		b.WriteString(violation.String())
	}
	if rest := len(invalid.Violations) - len(shown); rest > 0 {
		fmt.Fprintf(b, "\n - and %d more", rest)
	}
	return truncate(b.String())
}

// truncate cuts a message to MaxMessageBytes, on a rune boundary so the result
// is still a valid string, and says that it did.
func truncate(msg string) string {
	if len(msg) <= MaxMessageBytes {
		return msg
	}
	cut := msg[:MaxMessageBytes]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "... (truncated)"
}
