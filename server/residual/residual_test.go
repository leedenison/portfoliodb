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
		// What the group's prices could be out by. Empty is none, which is what
		// every case here meant before the tolerance could be scaled.
		rounding string
		resolved []typev1.TxType
		want     typev1.AccountType
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
		// The case this exists for. 2676 units of a price printed to 2dp can be out
		// by 2676 * 0.005 = 13.38, and the sample export is out by 10.30 -- the
		// printed price failing to reproduce the cash row rather than a leg the
		// converter missed.
		{
			name:      "a large position at a price rounded to 2dp",
			commodity: "cur:GBP",
			amount:    "10.30",
			rounding:  "13.38",
			resolved:  []typev1.TxType{typev1.TxType_TRADE_ASSET},
			want:      typev1.AccountType_ACCOUNT_TYPE_SOURCE_ROUNDING,
		},
		// The bound is a bound. A residual past what the prices could account for
		// is still a leg the source did not supply, however large the position.
		{
			name:      "past what the prices could account for",
			commodity: "cur:GBP",
			amount:    "13.39",
			rounding:  "13.38",
			resolved:  []typev1.TxType{typev1.TxType_TRADE_ASSET},
			want:      typev1.AccountType_ACCOUNT_TYPE_IMBALANCE,
		},
		// A deposit run holds only money, whose price of 1 is exact, so nothing
		// scales its tolerance and it is still short of the leg that never came.
		{
			name:      "a journal, whose money carries no price rounding",
			commodity: "cur:GBP",
			amount:    "-5000",
			rounding:  "0",
			resolved:  []typev1.TxType{typev1.TxType_TRANSFER},
			want:      typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING,
		},
		// A residual in the security itself comes from a leg that was never
		// converted, so no price rounding reaches it whatever the group's other
		// legs were quoted to.
		{
			name:      "a security residual is not scaled by a price",
			commodity: "inst:abc",
			amount:    "0.5",
			rounding:  "13.38",
			resolved:  []typev1.TxType{typev1.TxType_TRADE_ASSET},
			want:      typev1.AccountType_ACCOUNT_TYPE_IMBALANCE,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rounding := decimal.Zero
			if tc.rounding != "" {
				rounding = decimal.RequireFromString(tc.rounding)
			}
			got := Type(tc.commodity, decimal.RequireFromString(tc.amount), rounding, tc.resolved)
			if got != tc.want {
				t.Fatalf("Type(%q, %s, rounding %s) = %v, want %v",
					tc.commodity, tc.amount, rounding, got, tc.want)
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

// The bound is moneyTolerance plus a non-negative term, so nothing can move from
// SOURCE_ROUNDING to IMBALANCE however the term is derived. That is what bounds the
// blast radius of scaling it.
func TestTolerance_NeverTightensMoney(t *testing.T) {
	base := Tolerance("cur:GBP", decimal.Zero)
	for _, r := range []string{"0", "0.0001", "13.38", "1000000"} {
		if got := Tolerance("cur:GBP", decimal.RequireFromString(r)); got.LessThan(base) {
			t.Fatalf("Tolerance with rounding %s = %s, below the money tolerance %s", r, got, base)
		}
	}
	// A negative term is a caller mistake rather than a tighter bound.
	if got := Tolerance("cur:GBP", decimal.RequireFromString("-5")); !got.Equal(base) {
		t.Fatalf("Tolerance with a negative rounding = %s, want the money tolerance %s", got, base)
	}
}
