package db

import (
	"testing"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
)

// The broker columns hold the enum's own name, so every defined broker has to
// survive a trip through the column and back.
func TestBrokerToStr_RoundTrips(t *testing.T) {
	for v, name := range typev1.Broker_name {
		b := typev1.Broker(v)
		if b == typev1.Broker_BROKER_UNSPECIFIED {
			continue
		}
		s := BrokerToStr(b)
		if s != name {
			t.Fatalf("BrokerToStr(%v) = %q, want %q", b, s, name)
		}
		if got := StrToBroker(s); got != b {
			t.Fatalf("StrToBroker(%q) = %v, want %v", s, got, b)
		}
	}
}

func TestBrokerToStr_UnknownIsEmpty(t *testing.T) {
	if got := BrokerToStr(typev1.Broker_BROKER_UNSPECIFIED); got != "" {
		t.Fatalf("unspecified = %q, want empty", got)
	}
	if got := BrokerToStr(typev1.Broker(9999)); got != "" {
		t.Fatalf("unknown value = %q, want empty", got)
	}
	if got := StrToBroker("DEFUNCTBROKER"); got != typev1.Broker_BROKER_UNSPECIFIED {
		t.Fatalf("unknown string = %v, want unspecified", got)
	}
}

func TestValidCurrencyCode(t *testing.T) {
	for _, s := range []string{"GBP", "USD", "JPY"} {
		if !ValidCurrencyCode(s) {
			t.Fatalf("%q rejected", s)
		}
	}
	for _, s := range []string{"", "gbp", "GB", "GBPX", "GB1", "GBP "} {
		if ValidCurrencyCode(s) {
			t.Fatalf("%q accepted", s)
		}
	}
}
