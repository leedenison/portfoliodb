package identifier

import "testing"

// The separator is the whole point of Key, so this is the test that holds it to
// its job: triples that differ only in where the boundary between domain and
// value falls must not share a key. Under any printable separator these two
// would join to one string, and the resolve cache would hand one instrument's
// entry to the other.
func TestKeySeparatesComponents(t *testing.T) {
	a := Identifier{Type: "MIC_TICKER", Domain: "", Value: "XNAS:AAPL"}
	b := Identifier{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}
	if a.Key() == b.Key() {
		t.Errorf("Key() collided for %v and %v: both %q", a, b, a.Key())
	}
}

func TestKey(t *testing.T) {
	for _, tt := range []struct {
		name string
		id   Identifier
		want string
	}{
		{"full triple", Identifier{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}, "MIC_TICKER\x00XNAS\x00AAPL"},
		{"no domain", Identifier{Type: "ISIN", Value: "US0378331005"}, "ISIN\x00\x00US0378331005"},
		{"zero value", Identifier{}, "\x00\x00"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.id.Key(); got != tt.want {
				t.Errorf("Key() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestString(t *testing.T) {
	for _, tt := range []struct {
		name string
		id   Identifier
		want string
	}{
		{"full triple", Identifier{Type: "MIC_TICKER", Domain: "XNAS", Value: "AAPL"}, "MIC_TICKER:XNAS:AAPL"},
		{"no domain", Identifier{Type: "ISIN", Value: "US0378331005"}, "ISIN::US0378331005"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.id.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
