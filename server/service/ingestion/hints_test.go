package ingestion

import (
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
)

func TestTxTypeToSecurityType(t *testing.T) {
	tests := []struct {
		txType apiv1.TxType
		want   string
	}{
		{apiv1.TxType_JRNLFUND, SecurityTypeCash},
		{apiv1.TxType_JRNLSEC, SecurityTypeEquity},
		{apiv1.TxType_SPLIT, SecurityTypeNone},
		{apiv1.TxType_BUYSTOCK, SecurityTypeEquity},
		{apiv1.TxType_INCOME, SecurityTypeCash},
		{apiv1.TxType_BUYOPT, SecurityTypeOption},
	}
	for _, tt := range tests {
		got := TxTypeToSecurityType(tt.txType)
		if got != tt.want {
			t.Errorf("TxTypeToSecurityType(%v) = %q, want %q", tt.txType, got, tt.want)
		}
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
		if h.Currency != "" || h.SecurityType != "" {
			t.Errorf("expected empty hints, got %+v", h)
		}
	})
}
