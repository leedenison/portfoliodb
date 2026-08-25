package ingestion

import (
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/pluginutil"
	"testing"
)

func TestHintsFromTx_AssetClassHint(t *testing.T) {
	tests := []struct {
		name     string
		hint     typev1.AssetClass
		wantHint string
	}{
		{"stated CASH", typev1.AssetClass_CASH, identifier.SecurityTypeHintCash},
		{"stated STOCK", typev1.AssetClass_STOCK, identifier.SecurityTypeHintStock},
		{"stated OPTION", typev1.AssetClass_OPTION, identifier.SecurityTypeHintOption},
		{"stated EQUITY is carried as stated", typev1.AssetClass_EQUITY, identifier.SecurityTypeHintEquity},
		// Both of these route as a security: cash plugins run only for a stated
		// class under CASH, and neither of these is one.
		{"stated UNKNOWN rules nothing out and is floored",
			typev1.AssetClass_UNKNOWN, identifier.SecurityTypeHintSecurity},
		{"unset states no claim and is floored",
			typev1.AssetClass_ASSET_CLASS_UNSPECIFIED, identifier.SecurityTypeHintSecurity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := HintsFromTx(&apiv1.Tx{AssetClassHint: tt.hint})
			if h.SecurityTypeHint != tt.wantHint {
				t.Errorf("SecurityTypeHint = %q, want %q", h.SecurityTypeHint, tt.wantHint)
			}
		})
	}
}

// A floored hint reaches the security plugins and no cash plugin, which is the
// whole reason for the floor.
func TestHintsFromTx_FlooredHintRoutesAsASecurity(t *testing.T) {
	h := HintsFromTx(&apiv1.Tx{AssetClassHint: typev1.AssetClass_ASSET_CLASS_UNSPECIFIED})
	cash := map[string]bool{identifier.SecurityTypeHintCash: true}
	stocks := map[string]bool{identifier.SecurityTypeHintStock: true}
	if pluginutil.AcceptsSecurityType(cash, h.SecurityTypeHint) {
		t.Error("a row nobody called cash reached a cash plugin")
	}
	if !pluginutil.AcceptsSecurityType(stocks, h.SecurityTypeHint) {
		t.Error("a row stating only that it is a security was refused a security plugin")
	}
}

func TestHintsFromTx_Currency(t *testing.T) {
	t.Run("uses trading_currency as hint", func(t *testing.T) {
		tx := &apiv1.Tx{TradingCurrency: "GBP"}
		h := HintsFromTx(tx)
		if h.Currency != "GBP" {
			t.Errorf("Currency = %q, want GBP", h.Currency)
		}
	})
	t.Run("nil tx returns empty hints", func(t *testing.T) {
		h := HintsFromTx(nil)
		if h.Currency != "" || h.SecurityTypeHint != "" {
			t.Errorf("expected empty hints, got %+v", h)
		}
	})
}
