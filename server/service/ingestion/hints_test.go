package ingestion

import (
	"sort"
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
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
		{apiv1.TxType_BUYFUTURE, identifier.SecurityTypeHintFuture},
		{apiv1.TxType_SELLFUTURE, identifier.SecurityTypeHintFuture},
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

func TestAssetClassToTxTypes(t *testing.T) {
	t.Run("CASH maps to expected types", func(t *testing.T) {
		types := AssetClassToTxTypes(identifier.SecurityTypeHintCash)
		names := make([]string, len(types))
		for i, tt := range types {
			names[i] = tt.String()
		}
		sort.Strings(names)
		want := []string{"CASHFLOW", "INCOME", "INVEXPENSE", "JRNLFUND", "MARGININTEREST", "REINVEST", "RETOFCAP", "TRANSFER"}
		sort.Strings(want)
		if len(names) != len(want) {
			t.Fatalf("got %v, want %v", names, want)
		}
		for i := range names {
			if names[i] != want[i] {
				t.Errorf("index %d: got %q, want %q", i, names[i], want[i])
			}
		}
	})
	t.Run("FUTURE maps to BUYFUTURE and SELLFUTURE", func(t *testing.T) {
		types := AssetClassToTxTypes(identifier.SecurityTypeHintFuture)
		names := make([]string, len(types))
		for i, tt := range types {
			names[i] = tt.String()
		}
		sort.Strings(names)
		want := []string{"BUYFUTURE", "SELLFUTURE"}
		if len(names) != len(want) {
			t.Fatalf("got %v, want %v", names, want)
		}
		for i := range names {
			if names[i] != want[i] {
				t.Errorf("index %d: got %q, want %q", i, names[i], want[i])
			}
		}
	})
	t.Run("ETF has no mapped types", func(t *testing.T) {
		types := AssetClassToTxTypes(identifier.SecurityTypeHintETF)
		if len(types) != 0 {
			t.Errorf("expected empty, got %v", types)
		}
	})
}

func TestAssetClassToTxTypeStrings(t *testing.T) {
	strs := AssetClassToTxTypeStrings(identifier.SecurityTypeHintOption)
	sort.Strings(strs)
	want := []string{"BUYOPT", "CLOSUREOPT", "SELLOPT"}
	sort.Strings(want)
	if len(strs) != len(want) {
		t.Fatalf("got %v, want %v", strs, want)
	}
	for i := range strs {
		if strs[i] != want[i] {
			t.Errorf("index %d: got %q, want %q", i, strs[i], want[i])
		}
	}
}

func TestTxIgnored(t *testing.T) {
	rules := []db.IgnoredAssetClass{
		{Broker: "IBKR", Account: "", AssetClass: "CASH"},
		{Broker: "SCHB", Account: "ACC-1", AssetClass: "OPTION"},
	}

	tests := []struct {
		name   string
		tx     *apiv1.Tx
		broker string
		want   bool
	}{
		{
			name:   "CASH tx for IBKR is ignored (broker-level)",
			tx:     &apiv1.Tx{Type: apiv1.TxType_CASHFLOW, Account: "ANY"},
			broker: "IBKR",
			want:   true,
		},
		{
			name:   "CASH tx for IBKR different account still ignored",
			tx:     &apiv1.Tx{Type: apiv1.TxType_INCOME, Account: "ACC-2"},
			broker: "IBKR",
			want:   true,
		},
		{
			name:   "STOCK tx for IBKR is not ignored",
			tx:     &apiv1.Tx{Type: apiv1.TxType_BUYSTOCK, Account: "ACC-1"},
			broker: "IBKR",
			want:   false,
		},
		{
			name:   "OPTION tx for SCHB ACC-1 is ignored (account-level)",
			tx:     &apiv1.Tx{Type: apiv1.TxType_BUYOPT, Account: "ACC-1"},
			broker: "SCHB",
			want:   true,
		},
		{
			name:   "OPTION tx for SCHB ACC-2 is NOT ignored",
			tx:     &apiv1.Tx{Type: apiv1.TxType_BUYOPT, Account: "ACC-2"},
			broker: "SCHB",
			want:   false,
		},
		{
			name:   "Fidelity tx is not ignored (no rules)",
			tx:     &apiv1.Tx{Type: apiv1.TxType_CASHFLOW, Account: "ACC-1"},
			broker: "Fidelity",
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TxIgnored(tt.tx, tt.broker, rules)
			if got != tt.want {
				t.Errorf("TxIgnored() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("empty rules never ignores", func(t *testing.T) {
		tx := &apiv1.Tx{Type: apiv1.TxType_CASHFLOW}
		if TxIgnored(tx, "IBKR", nil) {
			t.Error("expected false with nil rules")
		}
	})
}
