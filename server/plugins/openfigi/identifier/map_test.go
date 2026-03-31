package identifier

import (
	"testing"

	"github.com/leedenison/portfoliodb/server/db"
)

func TestClassify_SpecExamples(t *testing.T) {
	tests := []struct {
		name         string
		secType      string
		secType2     string
		marketSector string
		want         string
	}{
		{"common stock", "Common Stock", "Common Stock", "Equity", db.AssetClassStock},
		{"equity option", "Equity Option", "Option", "Equity", db.AssetClassOption},
		{"etp", "ETP", "ETP", "Equity", db.AssetClassETF},
		{"bond corp", "Bond", "Corp", "Corp", db.AssetClassFixedIncome},
		{"open-end fund", "Open-End Fund", "Fund", "Equity", db.AssetClassMutualFund},
		{"single stock future", "SINGLE STOCK FUTURE", "Future", "Equity", db.AssetClassFuture},
		{"currency spot", "Currency spot", "Spot", "Curncy", db.AssetClassFX},
		// Spec shows this as UNKNOWN, but with marketSector=Equity the STOCK
		// catch-all at priority 702 fires. Use a non-matching marketSector to
		// exercise the UNKNOWN fallback; Equity case tested separately below.
		{"index wrt unknown", "Index WRT", "Warrant", "Other", db.AssetClassUnknown},
		// With Equity marketSector, warrants fall into STOCK via the broad catch-all.
		{"index wrt equity", "Index WRT", "Warrant", "Equity", db.AssetClassStock},
		{"abs auto pool mtge", "ABS Auto", "Pool", "Mtge", db.AssetClassFixedIncome},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classify(tt.secType, tt.secType2, tt.marketSector)
			if got != tt.want {
				t.Errorf("classify(%q, %q, %q) = %q, want %q",
					tt.secType, tt.secType2, tt.marketSector, got, tt.want)
			}
		})
	}
}

func TestClassify_PriorityEdgeCases(t *testing.T) {
	tests := []struct {
		name         string
		secType      string
		secType2     string
		marketSector string
		want         string
	}{
		// Currency Option -> OPTION, not FX
		{"currency option is option", "Currency Option", "Option", "Curncy", db.AssetClassOption},
		// Option on Equity Future -> OPTION, not FUTURE
		{"option on equity future", "Option on Equity Future", "Option", "Equity", db.AssetClassOption},
		// ETP with securityType2=Mutual Fund -> ETF (ETF rule comes before MUTUAL_FUND)
		{"etp mutual fund is etf", "ETP", "Mutual Fund", "Equity", db.AssetClassETF},
		// Curncy marketSector without FX securityType -> FX via marketSector rule
		{"curncy market sector", "Some Type", "Some Type2", "Curncy", db.AssetClassFX},
		// Corp marketSector -> FIXED_INCOME, not STOCK
		{"corp market sector", "Some Type", "Some Type2", "Corp", db.AssetClassFixedIncome},
		// Equity marketSector with unknown securityType -> STOCK
		{"equity market sector fallback", "Some Type", "Some Type2", "Equity", db.AssetClassStock},
		// FUTURE securityType2 -> FUTURE
		{"future via securityType2", "Some Type", "Future", "Equity", db.AssetClassFuture},
		// Fund securityType2 -> MUTUAL_FUND
		{"fund via securityType2", "Some Type", "Fund", "Equity", db.AssetClassMutualFund},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classify(tt.secType, tt.secType2, tt.marketSector)
			if got != tt.want {
				t.Errorf("classify(%q, %q, %q) = %q, want %q",
					tt.secType, tt.secType2, tt.marketSector, got, tt.want)
			}
		})
	}
}

func TestClassify_CaseInsensitive(t *testing.T) {
	tests := []struct {
		name         string
		secType      string
		secType2     string
		marketSector string
		want         string
	}{
		{"lowercase", "common stock", "common stock", "equity", db.AssetClassStock},
		{"uppercase", "COMMON STOCK", "COMMON STOCK", "EQUITY", db.AssetClassStock},
		{"mixed case", "Common Stock", "Common Stock", "Equity", db.AssetClassStock},
		{"etp lowercase", "etp", "etp", "equity", db.AssetClassETF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classify(tt.secType, tt.secType2, tt.marketSector)
			if got != tt.want {
				t.Errorf("classify(%q, %q, %q) = %q, want %q",
					tt.secType, tt.secType2, tt.marketSector, got, tt.want)
			}
		})
	}
}

func TestClassify_EmptyAndWhitespace(t *testing.T) {
	tests := []struct {
		name         string
		secType      string
		secType2     string
		marketSector string
		want         string
	}{
		{"all empty", "", "", "", db.AssetClassUnknown},
		{"all whitespace", "  ", "  ", "  ", db.AssetClassUnknown},
		{"whitespace padded match", " ETP ", " ETP ", " Equity ", db.AssetClassETF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classify(tt.secType, tt.secType2, tt.marketSector)
			if got != tt.want {
				t.Errorf("classify(%q, %q, %q) = %q, want %q",
					tt.secType, tt.secType2, tt.marketSector, got, tt.want)
			}
		})
	}
}

func TestClassify_EachAssetClass(t *testing.T) {
	tests := []struct {
		name         string
		secType      string
		secType2     string
		marketSector string
		want         string
	}{
		{"option", "Equity Option", "", "", db.AssetClassOption},
		{"future", "SINGLE STOCK FUTURE", "", "", db.AssetClassFuture},
		{"etf", "ETP", "", "", db.AssetClassETF},
		{"fx", "Currency spot", "", "", db.AssetClassFX},
		{"fixed income", "Bond", "", "", db.AssetClassFixedIncome},
		{"mutual fund", "Open-End Fund", "", "", db.AssetClassMutualFund},
		{"stock", "Common Stock", "", "", db.AssetClassStock},
		{"cash", "CASH", "", "", db.AssetClassCash},
		{"unknown", "Something Exotic", "Weird", "Mars", db.AssetClassUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classify(tt.secType, tt.secType2, tt.marketSector)
			if got != tt.want {
				t.Errorf("classify(%q, %q, %q) = %q, want %q",
					tt.secType, tt.secType2, tt.marketSector, got, tt.want)
			}
		})
	}
}
