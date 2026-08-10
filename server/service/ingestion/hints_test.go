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
			tx:     &apiv1.Tx{AssetClassHint: typev1.AssetClass_CASH, Account: "ANY"},
			broker: "IBKR",
			want:   true,
		},
		{
			name:   "CASH tx for IBKR different account still ignored",
			tx:     &apiv1.Tx{AssetClassHint: typev1.AssetClass_CASH, Account: "ACC-2"},
			broker: "IBKR",
			want:   true,
		},
		{
			name:   "STOCK tx for IBKR is not ignored",
			tx:     &apiv1.Tx{AssetClassHint: typev1.AssetClass_STOCK, Account: "ACC-1"},
			broker: "IBKR",
			want:   false,
		},
		{
			name:   "OPTION tx for SCHB ACC-1 is ignored (account-level)",
			tx:     &apiv1.Tx{AssetClassHint: typev1.AssetClass_OPTION, Account: "ACC-1"},
			broker: "SCHB",
			want:   true,
		},
		{
			name:   "OPTION tx for SCHB ACC-2 is NOT ignored",
			tx:     &apiv1.Tx{AssetClassHint: typev1.AssetClass_OPTION, Account: "ACC-2"},
			broker: "SCHB",
			want:   false,
		},
		{
			name:   "Fidelity tx is not ignored (no rules)",
			tx:     &apiv1.Tx{AssetClassHint: typev1.AssetClass_CASH, Account: "ACC-1"},
			broker: "FIDELITY",
			want:   false,
		},
		{
			// The rules match the stated hint only: a row with no claim matches
			// no rule at ingest, whatever it later resolves to.
			name:   "hintless row matches no rule",
			tx:     &apiv1.Tx{Account: "ACC-1"},
			broker: "IBKR",
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
		tx := &apiv1.Tx{AssetClassHint: typev1.AssetClass_CASH}
		if TxIgnored(tx, "IBKR", nil) {
			t.Error("expected false with nil rules")
		}
	})
}
