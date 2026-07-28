package postgres

import (
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
)

// The stored form of a broker is its enum name, and every defined broker must
// survive a round trip. FIDELITY was previously stored as "Fidelity", which
// strToBroker recognised but the e2e fixtures did not -- fixture rows spelled
// "FIDELITY" came back as BROKER_UNSPECIFIED.
func TestBrokerStorageRoundTrip(t *testing.T) {
	for value, name := range apiv1.Broker_name {
		broker := apiv1.Broker(value)
		if broker == apiv1.Broker_BROKER_UNSPECIFIED {
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
		broker apiv1.Broker
	}{
		{"unspecified", apiv1.Broker_BROKER_UNSPECIFIED},
		{"undefined value", apiv1.Broker(99)},
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
		if got := strToBroker(s); got != apiv1.Broker_BROKER_UNSPECIFIED {
			t.Errorf("strToBroker(%q): want BROKER_UNSPECIFIED, got %v", s, got)
		}
	}
}
