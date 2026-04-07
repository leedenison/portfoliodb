package db

import "testing"

func TestIsAssetClassCompatible(t *testing.T) {
	tests := []struct {
		name     string
		implied  string
		resolved string
		want     bool
	}{
		// Empty resolved class is always compatible (no signal to contradict).
		{"empty resolved with stock implied", AssetClassStock, "", true},
		{"empty resolved with cash implied", AssetClassCash, "", true},
		{"empty resolved with unknown implied", AssetClassUnknown, "", true},

		// Exact matches.
		{"stock=stock", AssetClassStock, AssetClassStock, true},
		{"option=option", AssetClassOption, AssetClassOption, true},
		{"cash=cash", AssetClassCash, AssetClassCash, true},

		// STOCK <-> ETF equivalence.
		{"stock<->etf", AssetClassStock, AssetClassETF, true},
		{"etf<->stock", AssetClassETF, AssetClassStock, true},

		// MUTUAL_FUND <-> ETF equivalence.
		{"mf<->etf", AssetClassMutualFund, AssetClassETF, true},
		{"etf<->mf", AssetClassETF, AssetClassMutualFund, true},

		// Non-transitivity guard: STOCK and MUTUAL_FUND must remain incompatible.
		{"stock vs mf rejected", AssetClassStock, AssetClassMutualFund, false},
		{"mf vs stock rejected", AssetClassMutualFund, AssetClassStock, false},

		// Concrete mismatches.
		{"stock vs option", AssetClassStock, AssetClassOption, false},
		{"option vs future", AssetClassOption, AssetClassFuture, false},
		{"income (cash) vs stock", AssetClassCash, AssetClassStock, false},
		{"income (cash) vs mf", AssetClassCash, AssetClassMutualFund, false},
		{"buystock vs cash", AssetClassStock, AssetClassCash, false},

		// UNKNOWN-implied tx types: any concrete class except CASH is allowed.
		{"unknown vs stock allowed", AssetClassUnknown, AssetClassStock, true},
		{"unknown vs etf allowed", AssetClassUnknown, AssetClassETF, true},
		{"unknown vs option allowed", AssetClassUnknown, AssetClassOption, true},
		{"unknown vs mf allowed", AssetClassUnknown, AssetClassMutualFund, true},
		{"unknown vs cash rejected", AssetClassUnknown, AssetClassCash, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsAssetClassCompatible(tc.implied, tc.resolved)
			if got != tc.want {
				t.Errorf("IsAssetClassCompatible(%q, %q) = %v, want %v", tc.implied, tc.resolved, got, tc.want)
			}
		})
	}
}
