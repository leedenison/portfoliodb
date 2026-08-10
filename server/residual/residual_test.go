package residual

import (
	"testing"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/shopspring/decimal"
)

func TestType(t *testing.T) {
	tests := []struct {
		name      string
		commodity string
		amount    string
		resolved  []typev1.TxType
		want      typev1.AccountType
	}{
		{
			name:      "missing cash leg",
			commodity: "cur:USD",
			amount:    "-1855",
			resolved:  []typev1.TxType{typev1.TxType_TRADE_ASSET},
			want:      typev1.AccountType_ACCOUNT_TYPE_IMBALANCE,
		},
		{
			name:      "one-sided journal",
			commodity: "cur:USD",
			amount:    "-500",
			resolved:  []typev1.TxType{typev1.TxType_TRANSFER},
			want:      typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING,
		},
		{
			name:      "internal transfer routes to clearing",
			commodity: "cur:USD",
			amount:    "-500",
			resolved:  []typev1.TxType{typev1.TxType_TRANSFER_INTERNAL},
			want:      typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING,
		},
		{
			name:      "external transfer routes to clearing",
			commodity: "cur:USD",
			amount:    "-500",
			resolved:  []typev1.TxType{typev1.TxType_TRANSFER_EXTERNAL},
			want:      typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING,
		},
		{
			name:      "one transfer leg in a mixed group",
			commodity: "cur:USD",
			amount:    "-500",
			resolved:  []typev1.TxType{typev1.TxType_EXPENSE, typev1.TxType_TRANSFER},
			want:      typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING,
		},
		{
			// A cross-branch declared set resolves to AMBIGUOUS, which is not a
			// transfer under every reading, so its residual is a missing leg.
			name:      "ambiguous resolution",
			commodity: "cur:USD",
			amount:    "-500",
			resolved:  []typev1.TxType{typev1.TxType_AMBIGUOUS},
			want:      typev1.AccountType_ACCOUNT_TYPE_IMBALANCE,
		},
		{
			name:      "sub-cent difference",
			commodity: "cur:USD",
			amount:    "0.0028",
			resolved:  []typev1.TxType{typev1.TxType_TRADE_ASSET},
			want:      typev1.AccountType_ACCOUNT_TYPE_SOURCE_ROUNDING,
		},
		{
			// The tolerance is exclusive: half a cent is what a 2dp source can be
			// out by, so a residual of exactly that is a leg rather than rounding.
			name:      "exactly at the money tolerance",
			commodity: "cur:USD",
			amount:    "0.005",
			resolved:  []typev1.TxType{typev1.TxType_TRADE_ASSET},
			want:      typev1.AccountType_ACCOUNT_TYPE_IMBALANCE,
		},
		{
			// Rounding beats the transfer classification rather than the other way
			// round: a journal the source rounded is still rounding.
			name:      "sub-cent difference on a journal",
			commodity: "cur:USD",
			amount:    "-0.001",
			resolved:  []typev1.TxType{typev1.TxType_TRANSFER},
			want:      typev1.AccountType_ACCOUNT_TYPE_SOURCE_ROUNDING,
		},
		{
			name:      "shares the source left out",
			commodity: "inst:11111111-1111-1111-1111-111111111111",
			amount:    "10",
			resolved:  []typev1.TxType{typev1.TxType_TRADE_ASSET},
			want:      typev1.AccountType_ACCOUNT_TYPE_IMBALANCE,
		},
		{
			// A security's tolerance is 1e-6, not half a cent, so a fraction of a
			// share well below a cent is still a real quantity.
			name:      "fraction of a share",
			commodity: "inst:11111111-1111-1111-1111-111111111111",
			amount:    "0.001",
			resolved:  []typev1.TxType{typev1.TxType_TRADE_ASSET},
			want:      typev1.AccountType_ACCOUNT_TYPE_IMBALANCE,
		},
		{
			name:      "sub-tolerance quantity",
			commodity: "inst:11111111-1111-1111-1111-111111111111",
			amount:    "-0.0000001",
			resolved:  []typev1.TxType{typev1.TxType_TRANSFER},
			want:      typev1.AccountType_ACCOUNT_TYPE_SOURCE_ROUNDING,
		},
		{
			// An unresolved commodity is weighed against itself, and takes the
			// security tolerance because it is not money.
			name:      "unresolved commodity",
			commodity: "desc:MYSTERY HOLDING",
			amount:    "0.001",
			resolved:  []typev1.TxType{typev1.TxType_TRADE_CASH},
			want:      typev1.AccountType_ACCOUNT_TYPE_IMBALANCE,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Type(tc.commodity, decimal.RequireFromString(tc.amount), tc.resolved)
			if got != tc.want {
				t.Fatalf("Type(%q, %s) = %v, want %v", tc.commodity, tc.amount, got, tc.want)
			}
		})
	}
}

func TestCommodityAccessors(t *testing.T) {
	if c, ok := CurrencyOf("cur:USD"); !ok || c != "USD" {
		t.Fatalf("CurrencyOf(cur:USD) = %q, %v; want USD, true", c, ok)
	}
	if _, ok := CurrencyOf("inst:abc"); ok {
		t.Fatal("CurrencyOf(inst:abc) reported money")
	}
	if id, ok := InstrumentOf("inst:abc"); !ok || id != "abc" {
		t.Fatalf("InstrumentOf(inst:abc) = %q, %v; want abc, true", id, ok)
	}
	if _, ok := InstrumentOf("desc:inst:abc"); ok {
		t.Fatal("InstrumentOf(desc:inst:abc) reported a security")
	}
}
