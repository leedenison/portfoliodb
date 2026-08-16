package grouping

import (
	"testing"
	"time"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func decp(s string) *decimal.Decimal {
	d := dec(s)
	return &d
}

func day(d int) time.Time {
	return time.Date(2022, 2, d, 10, 0, 0, 0, time.UTC)
}

// security builds a trade's asset leg: a share count, the price the source quoted,
// and the cash total it stated for the row.
func security(id string, qty, price, stated string, d int, ref int64) db.GroupingPosting {
	p := posting(id, typev1.TxType_TRADE_ASSET)
	p.Quantity = dec(qty)
	p.UnitPrice = decp(price)
	p.SettlementAmount = decp(stated)
	p.InstrumentID = "inst-1"
	p.OrderDate = day(d)
	return withRef(p, ref)
}

// money builds a cash posting: the quantity is the amount, signed, and there is no
// settlement amount because the quantity already is one.
func money(id, qty string, d int, ref int64, declared ...typev1.TxType) db.GroupingPosting {
	p := posting(id, declared...)
	p.Quantity = dec(qty)
	p.InstrumentID = "gbp"
	p.OrderDate = day(d)
	return withRef(p, ref)
}

// withRef stamps a Fidelity-shaped reference: the cell verbatim as the token, the
// number it carries as the ordinal, and the span the source declares.
func withRef(p db.GroupingPosting, ref int64) db.GroupingPosting {
	if ref == 0 {
		return p
	}
	span := int64(8)
	ordinal := ref
	p.Correlations = append(p.Correlations, db.Correlation{
		Token:       decimal.NewFromInt(ref).String(),
		Ordinal:     &ordinal,
		OrdinalSpan: &span,
		Scope:       db.ScopeFile,
		Match:       []string{db.MatchExact, db.MatchOrdinal},
	})
	return p
}

func tradeRules() []Rule { return []Rule{Disposal(), Acquisition(), CashTrade()} }

func TestTrade(t *testing.T) {
	tests := []struct {
		name string
		ps   []db.GroupingPosting
		want [][]string
	}{
		{
			// 2676 units at a quoted 7.67 is 20524.92, while the source states
			// 20514.62 -- 0.05% out, which is the rounding in the price it printed.
			name: "pairs a sale with the cash that came in for it",
			ps: []db.GroupingPosting{
				security("a", "-2676", "7.67", "20514.62", 10, 795832439),
				money("b", "20514.62", 10, 795832440, typev1.TxType_TRADE_CASH),
			},
			want: [][]string{{"a", "b"}},
		},
		{
			// The purchase's stated total carries the dealing fee; the cash row is
			// the consideration alone, so the two differ by the charge.
			name: "pairs a purchase across the fee gap",
			ps: []db.GroupingPosting{
				security("a", "585", "7.67", "4497.98", 10, 795832441),
				money("b", "-4487.98", 10, 795832442, typev1.TxType_TRADE_CASH),
			},
			want: [][]string{{"a", "b"}},
		},
		{
			// A fee cannot be negative, so a cash row larger than the purchase's
			// own total belongs to a different trade.
			name: "refuses a purchase whose cash row exceeds its total",
			ps: []db.GroupingPosting{
				security("a", "585", "7.67", "4487.98", 10, 795832441),
				money("b", "-4497.98", 10, 795832442, typev1.TxType_TRADE_CASH),
			},
			want: [][]string{{"a"}, {"b"}},
		},
		{
			// The amounts agree to the penny but 8000 units at 3.75 is 30000,
			// which the cash row misses by whole percentage points. The
			// cross-check is what catches a cash row that belongs elsewhere.
			name: "rejects a cash row inconsistent with quantity times price",
			ps: []db.GroupingPosting{
				security("a", "-8000", "3.75", "8000", 10, 795832439),
				money("b", "8000", 10, 795832440, typev1.TxType_TRADE_CASH),
			},
			want: [][]string{{"a"}, {"b"}},
		},
		{
			name: "does not pair across accounts",
			ps: []db.GroupingPosting{
				security("a", "-2676", "7.67", "20514.62", 10, 795832439),
				func() db.GroupingPosting {
					p := money("b", "20514.62", 10, 795832440, typev1.TxType_TRADE_CASH)
					p.Account = "A2"
					return p
				}(),
			},
			want: [][]string{{"a"}, {"b"}},
		},
		{
			name: "does not pair across settlement days",
			ps: []db.GroupingPosting{
				security("a", "-2676", "7.67", "20514.62", 10, 795832439),
				money("b", "20514.62", 11, 795832440, typev1.TxType_TRADE_CASH),
			},
			want: [][]string{{"a"}, {"b"}},
		},
		{
			// The money moved the wrong way for a sale, so it settles nothing here.
			name: "does not pair a sale with money going out",
			ps: []db.GroupingPosting{
				security("a", "-2676", "7.67", "20514.62", 10, 795832439),
				money("b", "-20514.62", 10, 795832440, typev1.TxType_TRADE_CASH),
			},
			want: [][]string{{"a"}, {"b"}},
		},
		{
			// A row the source said is definitely a transfer is not a trade's cash
			// leg under any reading, which is the may-be predicate doing real work.
			name: "does not take a row declared a transfer",
			ps: []db.GroupingPosting{
				security("a", "-2676", "7.67", "20514.62", 10, 795832439),
				money("b", "20514.62", 10, 795832440, typev1.TxType_TRANSFER),
			},
			want: [][]string{{"a"}, {"b"}},
		},
		{
			// Fidelity's Cash In is a trade's cash leg or a transfer and the
			// wording cannot tell them apart, so a trade may still claim it -- and
			// claiming it is what settles which of the two it was.
			name: "takes a row that may be either",
			ps: []db.GroupingPosting{
				security("a", "-2676", "7.67", "20514.62", 10, 795832439),
				money("b", "20514.62", 10, 795832440, typev1.TxType_TRADE_CASH, typev1.TxType_TRANSFER),
			},
			want: [][]string{{"a", "b"}},
		},
		{
			// Both sides are money and neither can be an asset leg, so the trade
			// rules pass over them and the equal-and-opposite pair identifies
			// itself.
			name: "pairs a movement of the account's own cash",
			ps: []db.GroupingPosting{
				money("a", "-20000", 10, 791691783, typev1.TxType_TRADE_CASH),
				money("b", "20000", 10, 791691784, typev1.TxType_TRADE_CASH),
			},
			want: [][]string{{"a", "b"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := members(Partition(tc.ps, tradeRules(), DefaultOpts()))
			if !equal(got, tc.want) {
				t.Fatalf("partition = %v, want %v", got, tc.want)
			}
		})
	}
}

// Two sales of the same size on one day are told apart by which cash row each
// broker reference sits next to. The converter's sell pass takes the first candidate
// in row order and would depend on the export's ordering here; ranking on the
// evidence does not.
func TestTrade_DoesNotCrossPairTwoSalesOnOneDay(t *testing.T) {
	ps := []db.GroupingPosting{
		security("sell1", "-1000", "10", "10000", 10, 500000100),
		money("cash1", "10000", 10, 500000101, typev1.TxType_TRADE_CASH),
		security("sell2", "-1000", "10", "10000", 10, 500000200),
		money("cash2", "10000", 10, 500000201, typev1.TxType_TRADE_CASH),
	}
	got := members(Partition(ps, tradeRules(), DefaultOpts()))
	want := [][]string{{"cash1", "sell1"}, {"cash2", "sell2"}}
	if !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// Candidates are ranked across the whole neighbourhood before any claim is made, so
// a purchase whose cash row another purchase could also use does not lose it to
// whichever happened to be considered first.
//
// Two identical purchases and two identical cash rows: on amount alone every pairing
// is feasible, so nothing but the references says which belongs to which. Taken in
// slice order the first purchase would take whichever cash row it met first and the
// second would take what was left, giving gaps of 10 and 8; ranked, each takes the
// row issued beside it, giving 1 and 1.
//
// Best-first is greedy rather than globally optimal, which is what the converter
// does too. It is the ranking that carries the weight, not the search.
func TestTrade_OneBuyDoesNotStrandAnother(t *testing.T) {
	ps := []db.GroupingPosting{
		security("buy2", "100", "10", "1002", 10, 110),
		security("buy1", "100", "10", "1002", 10, 101),
		money("cashA", "-1000", 10, 102, typev1.TxType_TRADE_CASH),
		money("cashB", "-1000", 10, 111, typev1.TxType_TRADE_CASH),
	}
	got := members(Partition(ps, tradeRules(), DefaultOpts()))
	want := [][]string{{"buy1", "cashA"}, {"buy2", "cashB"}}
	if !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// A disposal's test is equality and an acquisition's an inequality, so the weaker
// test must not take a row the stronger one identifies outright.
func TestTrade_DisposalClaimsBeforeAcquisition(t *testing.T) {
	// The cash out could settle the purchase, and it exactly matches the sale's
	// stated total. Precedence is what decides, not the order of the slice.
	ps := []db.GroupingPosting{
		security("sale", "-100", "10", "1000", 10, 800000010),
		money("cashIn", "1000", 10, 800000011, typev1.TxType_TRADE_CASH),
		security("buy", "100", "10", "1200", 10, 800000020),
		money("cashOut", "-1000", 10, 800000021, typev1.TxType_TRADE_CASH),
	}
	got := members(Partition(ps, tradeRules(), DefaultOpts()))
	want := [][]string{{"buy", "cashOut"}, {"cashIn", "sale"}}
	if !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// A security sale claims its cash in before a sale of the account's own cash can
// take it, which is what keeps the answer from depending on the order the broker
// exported the two.
func TestTrade_SecurityBeatsCashForTheSameRow(t *testing.T) {
	ps := []db.GroupingPosting{
		security("sale", "-100", "10", "1000", 10, 795832439),
		money("cashIn", "1000", 10, 795832440, typev1.TxType_TRADE_CASH),
		money("cashSale", "-1000", 10, 791691783, typev1.TxType_TRADE_CASH),
	}
	got := members(Partition(ps, tradeRules(), DefaultOpts()))
	want := [][]string{{"cashIn", "sale"}, {"cashSale"}}
	if !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// The rule that claims a posting resolves it, so a Cash In that paired with a sale
// is settled as that trade's cash leg rather than staying ambiguous.
func TestTrade_ClaimingResolvesTheType(t *testing.T) {
	ps := []db.GroupingPosting{
		security("a", "-2676", "7.67", "20514.62", 10, 795832439),
		money("b", "20514.62", 10, 795832440, typev1.TxType_TRADE_CASH, typev1.TxType_TRANSFER),
	}
	gs := Partition(ps, tradeRules(), DefaultOpts())

	if got := resolvedOf(gs, "a"); got != typev1.TxType_TRADE_ASSET {
		t.Fatalf("a resolved to %v, want TRADE_ASSET", got)
	}
	if got := resolvedOf(gs, "b"); got != typev1.TxType_TRADE_CASH {
		t.Fatalf("b resolved to %v, want TRADE_CASH", got)
	}
}

// A charge is dated on the order date while its trade settles later, and its money
// is accounted for by its own row, so nothing here should fold one into a trade.
func TestTrade_LeavesAChargeAlone(t *testing.T) {
	ps := []db.GroupingPosting{
		security("a", "-2676", "7.67", "20514.62", 10, 795832439),
		money("b", "20514.62", 10, 795832440, typev1.TxType_TRADE_CASH),
		money("fee", "-10.30", 10, 795832441, typev1.TxType_TRANSACTION_COST),
	}
	got := members(Partition(ps, tradeRules(), DefaultOpts()))
	want := [][]string{{"a", "b"}, {"fee"}}
	if !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// A source that stated the grouping outright is honoured first, and a trade rule may
// then add to what it named but may not take a member out of it.
func TestTrade_AddsToAGroupTheSourceNamed(t *testing.T) {
	fee := money("fee", "-10.30", 10, 0, typev1.TxType_TRANSACTION_COST)
	ps := []db.GroupingPosting{
		correlated(security("a", "-2676", "7.67", "20514.62", 10, 0), "", "fit1", db.ScopeAccount, db.MatchExact),
		correlated(money("b", "20514.62", 10, 0, typev1.TxType_TRADE_CASH), "", "fit1", db.ScopeAccount, db.MatchExact),
		fee,
	}
	rules := append([]Rule{Exact{}}, tradeRules()...)
	got := members(Partition(ps, rules, DefaultOpts()))
	// The trade rule cannot re-role a and b, so the pair the source named stands
	// and the fee stays outside it.
	want := [][]string{{"a", "b"}, {"fee"}}
	if !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

func TestTrade_IsOrderIndependent(t *testing.T) {
	ps := []db.GroupingPosting{
		security("sell1", "-1000", "10", "10000", 10, 500000100),
		money("cash1", "10000", 10, 500000101, typev1.TxType_TRADE_CASH),
		security("buy1", "100", "10", "1002", 10, 500000200),
		money("cash2", "-1000", 10, 500000201, typev1.TxType_TRADE_CASH),
		money("fee", "-2", 10, 500000202, typev1.TxType_TRANSACTION_COST),
	}
	want := members(Partition(ps, tradeRules(), DefaultOpts()))
	for i := range 20 {
		shuffled := make([]db.GroupingPosting, len(ps))
		copy(shuffled, ps)
		// Rotate rather than randomise: every starting position is covered and the
		// test states which orderings it checked.
		for j := range shuffled {
			shuffled[j] = ps[(j+i)%len(ps)]
		}
		if got := members(Partition(shuffled, tradeRules(), DefaultOpts())); !equal(got, want) {
			t.Fatalf("rotation %d: partition = %v, want %v", i, got, want)
		}
	}
}
