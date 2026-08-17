package ingestion

import (
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"testing"
)

func TestHintsFromTx_AssetClassHint(t *testing.T) {
	tests := []struct {
		name     string
		hint     typev1.AssetClass
		wantHint string
		wantKind string
	}{
		{"stated CASH", typev1.AssetClass_CASH, identifier.SecurityTypeHintCash, db.InstrumentKindCash},
		{"stated STOCK", typev1.AssetClass_STOCK, identifier.SecurityTypeHintStock, db.InstrumentKindSecurity},
		{"stated OPTION", typev1.AssetClass_OPTION, identifier.SecurityTypeHintOption, db.InstrumentKindSecurity},
		{"stated UNKNOWN is a security of unstated class", typev1.AssetClass_UNKNOWN, identifier.SecurityTypeHintUnknown, db.InstrumentKindSecurity},
		// No claim leaves the class unconstrained but still routes as a
		// security: cash plugins run only for a stated CASH.
		{"unset states no claim", typev1.AssetClass_ASSET_CLASS_UNSPECIFIED, "", db.InstrumentKindSecurity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := HintsFromTx(&apiv1.Tx{AssetClassHint: tt.hint})
			if h.SecurityTypeHint != tt.wantHint {
				t.Errorf("SecurityTypeHint = %q, want %q", h.SecurityTypeHint, tt.wantHint)
			}
			if h.InstrumentKind != tt.wantKind {
				t.Errorf("InstrumentKind = %q, want %q", h.InstrumentKind, tt.wantKind)
			}
		})
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
		if h.Currency != "" || h.SecurityTypeHint != "" || h.InstrumentKind != "" {
			t.Errorf("expected empty hints, got %+v", h)
		}
	})
}
