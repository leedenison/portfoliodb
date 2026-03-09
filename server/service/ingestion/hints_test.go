package ingestion

import (
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/identifier"
)

func TestTxTypeToSecurityTypeHint(t *testing.T) {
	tests := []struct {
		txType apiv1.TxType
		want   string
	}{
		{apiv1.TxType_JRNLFUND, identifier.SecurityTypeHintCash},
		{apiv1.TxType_JRNLSEC, identifier.SecurityTypeHintStock},
		{apiv1.TxType_SPLIT, identifier.SecurityTypeHintUnknown},
		{apiv1.TxType_BUYOTHER, identifier.SecurityTypeHintUnknown},
		{apiv1.TxType_SELLOTHER, identifier.SecurityTypeHintUnknown},
		{apiv1.TxType_BUYSTOCK, identifier.SecurityTypeHintStock},
		{apiv1.TxType_INCOME, identifier.SecurityTypeHintCash},
		{apiv1.TxType_BUYOPT, identifier.SecurityTypeHintOption},
		{apiv1.TxType_BUYDEBT, identifier.SecurityTypeHintFixedIncome},
		{apiv1.TxType_BUYMF, identifier.SecurityTypeHintMutualFund},
	}
	for _, tt := range tests {
		got := TxTypeToSecurityTypeHint(tt.txType)
		if got != tt.want {
			t.Errorf("TxTypeToSecurityTypeHint(%v) = %q, want %q", tt.txType, got, tt.want)
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
		if h.Currency != "" || h.SecurityTypeHint != "" {
			t.Errorf("expected empty hints, got %+v", h)
		}
	})
}
