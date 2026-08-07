package postgres

import (
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"testing"
)

// The stored form of a broker is its enum name, and every defined broker must
// survive a round trip. FIDELITY was previously stored as "Fidelity", which
// strToBroker recognised but the e2e fixtures did not -- fixture rows spelled
// "FIDELITY" came back as BROKER_UNSPECIFIED.
func TestBrokerStorageRoundTrip(t *testing.T) {
	for value, name := range typev1.Broker_name {
		broker := typev1.Broker(value)
		if broker == typev1.Broker_BROKER_UNSPECIFIED {
			continue
		}
		t.Run(name, func(t *testing.T) {
			got, err := brokerToStr(broker)
			if err != nil {
				t.Fatalf("brokerToStr(%v): %v", broker, err)
			}
			if got != name {
				t.Errorf("stored form: want %q, got %q", name, got)
			}
			if back := strToBroker(got); back != broker {
				t.Errorf("round trip: %v -> %q -> %v", broker, got, back)
			}
		})
	}
}

func TestBrokerToStr_Rejects(t *testing.T) {
	cases := []struct {
		name   string
		broker typev1.Broker
	}{
		{"unspecified", typev1.Broker_BROKER_UNSPECIFIED},
		{"undefined value", typev1.Broker(99)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := brokerToStr(tc.broker); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

func TestStrToBroker_UnknownIsUnspecified(t *testing.T) {
	// "Fidelity" was the old stored spelling; it must not resolve any more.
	for _, s := range []string{"", "Fidelity", "NOPE", "BROKER_UNSPECIFIED"} {
		if got := strToBroker(s); got != typev1.Broker_BROKER_UNSPECIFIED {
			t.Errorf("strToBroker(%q): want BROKER_UNSPECIFIED, got %v", s, got)
		}
	}
}
