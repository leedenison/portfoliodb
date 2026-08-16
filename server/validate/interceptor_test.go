package validate_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	ingestionv1 "github.com/leedenison/portfoliodb/proto/ingestion/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/testutil"
	"github.com/leedenison/portfoliodb/server/validate"
)

// request is an upsert of n postings, each of which violates one rule: an
// archive written against an older proto arrives with no declared type, which is
// the shape that made the message grow with the file.
func request(n int) *ingestionv1.UpsertTxsRequest {
	postings := make([]*archivev1.Posting, n)
	for i := range postings {
		postings[i] = &archivev1.Posting{
			Timestamp:             timestamppb.New(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)),
			InstrumentDescription: "GBP",
			Quantity:              "1",
		}
	}
	return &ingestionv1.UpsertTxsRequest{
		Window: &archivev1.TxWindow{
			Broker:       typev1.Broker_FIDELITY,
			PeriodFrom:   timestamppb.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
			PeriodBefore: timestamppb.New(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)),
			Source:       "Fidelity:web:standard",
			Postings:     postings,
		},
	}
}

func TestUnaryInterceptor(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatalf("protovalidate.New: %v", err)
	}
	handler := func(_ context.Context, req any) (any, error) { return req, nil }
	info := &grpc.UnaryServerInfo{FullMethod: "/portfoliodb.ingestion.v1.IngestionService/UpsertTxs"}
	interceptor := validate.UnaryInterceptor(validator)

	t.Run("valid request reaches the handler", func(t *testing.T) {
		req := request(1)
		req.Window.Postings[0].BrokerTxType = []typev1.TxType{typev1.TxType_TRADE_CASH}
		got, err := interceptor(context.Background(), req, info, handler)
		if err != nil {
			t.Fatalf("interceptor: %v", err)
		}
		if got != any(req) {
			t.Errorf("handler received %v, want the request", got)
		}
	})

	cases := []struct {
		name      string
		postings  int
		wantMore  string
		wantFirst string
	}{
		{
			name:      "one violation is stated in full",
			postings:  1,
			wantFirst: "window.postings[0].broker_tx_type",
		},
		{
			name:      "violations up to the cap are all listed",
			postings:  validate.MaxViolations,
			wantFirst: "window.postings[0].broker_tx_type",
		},
		{
			name:      "beyond the cap the rest are counted",
			postings:  validate.MaxViolations + 5,
			wantFirst: "window.postings[0].broker_tx_type",
			wantMore:  "and 5 more",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := interceptor(context.Background(), request(tc.postings), info, handler)
			testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
			msg := status.Convert(err).Message()
			if !strings.Contains(msg, tc.wantFirst) {
				t.Errorf("message %q does not name the first violation %q", msg, tc.wantFirst)
			}
			if tc.wantMore != "" && !strings.Contains(msg, tc.wantMore) {
				t.Errorf("message %q does not count the rest (%q)", msg, tc.wantMore)
			}
			if lines := strings.Count(msg, "\n - "); lines > validate.MaxViolations+1 {
				t.Errorf("message lists %d violations, want at most %d plus the count", lines, validate.MaxViolations)
			}
			if len(msg) > validate.MaxMessageBytes+len("... (truncated)") {
				t.Errorf("message is %d bytes, want at most %d", len(msg), validate.MaxMessageBytes)
			}
		})
	}
}

// The archive that produced the 503 this cap exists for: every posting of a
// large upload violating the same rule has to leave a message a proxy will
// forward.
func TestMessageStaysUnderTheHeaderBudget(t *testing.T) {
	validator, err := protovalidate.New()
	if err != nil {
		t.Fatalf("protovalidate.New: %v", err)
	}
	err = validator.Validate(request(1032))
	if err == nil {
		t.Fatal("expected a validation error")
	}
	if got, want := len(validate.Message(err)), validate.MaxMessageBytes; got > want {
		t.Errorf("message is %d bytes, want at most %d", got, want)
	}
	if raw := len(err.Error()); raw <= validate.MaxMessageBytes {
		t.Errorf("uncapped message is %d bytes, so this no longer tests the cap", raw)
	}
}

func TestMessagePassesThroughOtherErrors(t *testing.T) {
	if got := validate.Message(errors.New("boom")); got != "boom" {
		t.Errorf("Message = %q, want %q", got, "boom")
	}
	long := strings.Repeat("x", validate.MaxMessageBytes*2)
	if got := validate.Message(errors.New(long)); len(got) > validate.MaxMessageBytes+len("... (truncated)") {
		t.Errorf("Message is %d bytes, want it capped", len(got))
	}
}
