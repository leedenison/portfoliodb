package ingestion

import (
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
)

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
		if h.Currency != "" || h.SecurityType != "" {
			t.Errorf("expected empty hints, got %+v", h)
		}
	})
}
