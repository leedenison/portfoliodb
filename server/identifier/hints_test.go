package identifier

import "testing"

func TestUnderlyingSecTypeHint(t *testing.T) {
	// EQUITY: a listed option is written on a share or on a fund, and the
	// contract symbol does not say which.
	for _, c := range []string{SecurityTypeHintOption, SecurityTypeHintFuture} {
		if got := UnderlyingSecTypeHint(c); got != SecurityTypeHintEquity {
			t.Errorf("UnderlyingSecTypeHint(%q) = %q, want %q", c, got, SecurityTypeHintEquity)
		}
	}
	for _, c := range []string{SecurityTypeHintStock, SecurityTypeHintDerivative, SecurityTypeHintCash, ""} {
		if got := UnderlyingSecTypeHint(c); got != "" {
			t.Errorf("UnderlyingSecTypeHint(%q) = %q, want empty", c, got)
		}
	}
}
