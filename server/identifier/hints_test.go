package identifier

import "testing"

func TestShouldAttemptPlugin(t *testing.T) {
	cashTypes := map[string]bool{SecurityTypeHintCash: true}
	stockTypes := map[string]bool{SecurityTypeHintStock: true, SecurityTypeHintFixedIncome: true}

	tests := []struct {
		name       string
		acceptable map[string]bool
		secType    string
		want       bool
	}{
		// A plugin covering shares is offered what a statement line says, which
		// is the coarse value rather than the leaf below it.
		{"stock plugin accepts EQUITY", stockTypes, SecurityTypeHintEquity, true},
		{"stock plugin accepts STOCK", stockTypes, SecurityTypeHintStock, true},
		{"stock plugin accepts one of several declared", stockTypes, SecurityTypeHintFixedIncome, true},

		// A row whose source said only that it is a security is offered every
		// security plugin, and none of them has to enumerate the vocabulary to
		// be reached by it.
		{"stock plugin accepts SECURITY", stockTypes, SecurityTypeHintSecurity, true},

		// And the coarse value does not reach across the tree.
		{"cash plugin rejects a security", cashTypes, SecurityTypeHintSecurity, false},
		{"cash plugin rejects EQUITY", cashTypes, SecurityTypeHintEquity, false},
		{"stock plugin rejects CASH", stockTypes, SecurityTypeHintCash, false},
		{"cash plugin accepts CASH", cashTypes, SecurityTypeHintCash, true},

		// A class the plugin does not cover is refused however specific it is.
		{"stock plugin rejects OPTION", stockTypes, SecurityTypeHintOption, false},
		{"stock plugin rejects ETF", stockTypes, SecurityTypeHintETF, false},

		// The root reaches everything: it rules nothing out, so no plugin can
		// be said not to cover it.
		{"cash plugin accepts the root", cashTypes, SecurityTypeHintUnknown, true},
		{"stock plugin accepts the root", stockTypes, SecurityTypeHintUnknown, true},

		{"a plugin declaring nothing accepts everything", nil, SecurityTypeHintOption, true},
		{"a row stating nothing reaches every plugin", cashTypes, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldAttemptPlugin(tt.acceptable, tt.secType); got != tt.want {
				t.Errorf("ShouldAttemptPlugin(%v, %q) = %v, want %v", tt.acceptable, tt.secType, got, tt.want)
			}
		})
	}
}

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
