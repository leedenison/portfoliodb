package db

import "testing"

// The rule these three carry is tested in server/assetclass. What is tested
// here is that the stored spelling reaches it: a class the column holds is a
// value of the vocabulary, and one it does not hold is silence rather than a
// value of its own.
func TestAssetClassContradicts(t *testing.T) {
	tests := []struct {
		name             string
		stated, resolved string
		want             bool
	}{
		// Nothing to contradict. A missing resolved class is the case a row
		// whose instrument was deleted between resolution and validation hits.
		{"no resolved class", AssetClassStock, "", false},
		{"no stated class", "", AssetClassCash, false},
		{"a class outside the vocabulary reads as silence", AssetClassStock, "NOT_A_CLASS", false},

		// A claim that rules nothing out cannot be contradicted.
		{"stated root against a security", AssetClassUnknown, AssetClassStock, false},
		{"stated root against money", AssetClassUnknown, AssetClassCash, false},

		// A coarse claim admits every leaf under it, which is the whole reason
		// the coarse value exists: a statement line that cannot tell a share
		// from a fund has not disagreed with either.
		{"stated equity, resolved etf", AssetClassEquity, AssetClassETF, false},
		{"stated equity, resolved mutual fund", AssetClassEquity, AssetClassMutualFund, false},
		{"resolved coarser than stated", AssetClassETF, AssetClassEquity, false},
		{"stated security, resolved option", AssetClassSecurity, AssetClassOption, false},

		// And a specific claim means what it says.
		{"stated stock, resolved etf", AssetClassStock, AssetClassETF, true},
		{"stated stock, resolved mutual fund", AssetClassStock, AssetClassMutualFund, true},
		{"stated option, resolved future", AssetClassOption, AssetClassFuture, true},
		{"exact match", AssetClassStock, AssetClassStock, false},

		// Money and securities are disjoint at every depth, which is what keeps
		// a security from resolving to its trading currency.
		{"stated security, resolved cash", AssetClassSecurity, AssetClassCash, true},
		{"stated cash, resolved stock", AssetClassCash, AssetClassStock, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := AssetClassContradicts(tc.stated, tc.resolved); got != tc.want {
				t.Errorf("AssetClassContradicts(%q, %q) = %v, want %v", tc.stated, tc.resolved, got, tc.want)
			}
		})
	}
}

func TestAssetClassCorroborates(t *testing.T) {
	tests := []struct {
		name             string
		stated, resolved string
		want             bool
	}{
		{"exact match", AssetClassStock, AssetClassStock, true},
		{"an answer inside a coarse claim", AssetClassEquity, AssetClassETF, true},
		{"an answer coarser than the claim never reached the question",
			AssetClassStock, AssetClassEquity, false},
		{"a claim of the root rules nothing out", AssetClassUnknown, AssetClassStock, false},
		{"siblings", AssetClassStock, AssetClassETF, false},
		{"no stated class", "", AssetClassStock, false},
		{"no resolved class", AssetClassStock, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := AssetClassCorroborates(tc.stated, tc.resolved); got != tc.want {
				t.Errorf("AssetClassCorroborates(%q, %q) = %v, want %v", tc.stated, tc.resolved, got, tc.want)
			}
		})
	}
}

func TestAssetClassClaims(t *testing.T) {
	for _, s := range []string{AssetClassStock, AssetClassEquity, AssetClassSecurity, AssetClassCash} {
		if !AssetClassClaims(s) {
			t.Errorf("AssetClassClaims(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", AssetClassUnknown, "NOT_A_CLASS"} {
		if AssetClassClaims(s) {
			t.Errorf("AssetClassClaims(%q) = true, want false", s)
		}
	}
}

func TestIsDerivative(t *testing.T) {
	for _, s := range []string{AssetClassOption, AssetClassFuture} {
		if !IsDerivative(s) {
			t.Errorf("IsDerivative(%q) = false, want true", s)
		}
	}
	// DERIVATIVE itself is not one: the schema requires an underlying line so
	// that a strike can be read, and a security nobody resolved to a contract
	// carries no strike.
	for _, s := range []string{AssetClassDerivative, AssetClassStock, AssetClassCash, "", "NOT_A_CLASS"} {
		if IsDerivative(s) {
			t.Errorf("IsDerivative(%q) = true, want false", s)
		}
	}
}
