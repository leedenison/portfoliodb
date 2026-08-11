package grouping

import (
	"testing"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// The three rows a deposit into a product account is reported through, shaped as the
// converters emit them. The subscription is credited as a transfer, spent as a
// trade's cash leg, and credited again through the row Fidelity reports the same way
// whether money arrived from elsewhere or a switch's sale settled.
func lumpSum(id, qty string, d int, ref int64) db.GroupingPosting {
	return money(id, qty, d, ref, typev1.TxType_TRANSFER)
}

func spend(id, qty string, d int, ref int64) db.GroupingPosting {
	return money(id, qty, d, ref, typev1.TxType_TRADE_CASH)
}

func landing(id, qty string, d int, ref int64) db.GroupingPosting {
	return money(id, qty, d, ref, typev1.TxType_TRADE_CASH, typev1.TxType_TRANSFER)
}

func allRules() []Rule {
	return append(tradeRules(), Deposit())
}

func TestDeposit(t *testing.T) {
	tests := []struct {
		name string
		ps   []db.GroupingPosting
		want [][]string
	}{
		{
			name: "groups the run a deposit is reported through",
			ps: []db.GroupingPosting{
				lumpSum("open", "20000", 10, 971613428),
				spend("spent", "-20000", 10, 971613429),
				landing("landed", "20000", 10, 971613430),
			},
			want: [][]string{{"landed", "open", "spent"}},
		},
		{
			// The run ascends from the row that opened it, so the date the trade
			// rules bucket on would split this one. Reference proximity holds it.
			name: "groups a run whose rows settled on different days",
			ps: []db.GroupingPosting{
				lumpSum("open", "20000", 11, 559604931),
				spend("spent", "-20000", 11, 559604932),
				landing("landed", "20000", 14, 559604933),
			},
			want: [][]string{{"landed", "open", "spent"}},
		},
		{
			// Money paid in from outside with no run behind it stands on its own.
			name: "leaves a lump sum with no run alone",
			ps: []db.GroupingPosting{
				lumpSum("open", "20000", 10, 822942572),
			},
			want: [][]string{{"open"}},
		},
		{
			// A run climbs away from its opener, so a row numbered before it
			// belongs to something earlier.
			name: "does not take a row numbered before the opener",
			ps: []db.GroupingPosting{
				lumpSum("open", "20000", 10, 971613430),
				spend("earlier", "-20000", 10, 971613428),
			},
			want: [][]string{{"earlier"}, {"open"}},
		},
		{
			// The span travels with the evidence because how densely a broker
			// issues references is a fact about its numbering. Nine is past the
			// eight this source declares.
			name: "respects the span the source declared",
			ps: []db.GroupingPosting{
				lumpSum("open", "20000", 10, 971613428),
				spend("far", "-20000", 10, 971613437),
			},
			want: [][]string{{"far"}, {"open"}},
		},
		{
			name: "does not build a run across accounts",
			ps: []db.GroupingPosting{
				lumpSum("open", "20000", 10, 971613428),
				func() db.GroupingPosting {
					p := spend("spent", "-20000", 10, 971613429)
					p.Account = "A2"
					return p
				}(),
			},
			want: [][]string{{"open"}, {"spent"}},
		},
		{
			// The amount has to agree to the penny: a run is one sum moving through
			// the account, not two amounts that happen to be close.
			name: "does not take a row of a different amount",
			ps: []db.GroupingPosting{
				lumpSum("open", "20000", 10, 971613428),
				spend("other", "-19000", 10, 971613429),
			},
			want: [][]string{{"open"}, {"other"}},
		},
		{
			// Two deposits into one account, interleaved, differing by a pound.
			// Each takes the rows issued beside it.
			name: "separates two runs of nearly equal amount in one account",
			ps: []db.GroupingPosting{
				lumpSum("open1", "19996", 10, 700000100),
				spend("spent1", "-19996", 10, 700000101),
				landing("landed1", "19996", 10, 700000102),
				lumpSum("open2", "19995", 10, 700000103),
				spend("spent2", "-19995", 10, 700000104),
				landing("landed2", "19995", 10, 700000105),
			},
			want: [][]string{
				{"landed1", "open1", "spent1"},
				{"landed2", "open2", "spent2"},
			},
		},
		{
			// A second lump sum is a second deposit, not a further step in this
			// one, so it is never swallowed as a member.
			name: "does not take another lump sum as a member",
			ps: []db.GroupingPosting{
				lumpSum("open1", "20000", 10, 700000100),
				lumpSum("open2", "20000", 10, 700000101),
			},
			want: [][]string{{"open1"}, {"open2"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := members(Partition(tc.ps, allRules(), DefaultOpts()))
			if !equal(got, tc.want) {
				t.Fatalf("partition = %v, want %v", got, tc.want)
			}
		})
	}
}

// Deposit runs are built last, only ever from money rows the trade rules did not
// want, which is what stops a deposit taking the cash row of a trade of the same
// amount on the same day.
func TestDeposit_DoesNotTakeTheDaysTradeCashRow(t *testing.T) {
	ps := []db.GroupingPosting{
		// The purchase and the cash row that settles it, sitting inside the run's
		// reference span and carrying the same amount as the deposit.
		security("buy", "100", "200", "20000", 10, 971613493),
		spend("tradeCash", "-20000", 10, 971613494),
		lumpSum("open", "20000", 10, 971613492),
		landing("landed", "20000", 10, 971613495),
	}
	got := members(Partition(ps, allRules(), DefaultOpts()))
	// The trade keeps its cash row; the run is built from what is left.
	want := [][]string{{"buy", "tradeCash"}, {"landed", "open"}}
	if !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// The rule that claims a row resolves it. A deposit's opener and the money that
// lands are the movement; the subscription spent inside the account cannot be a
// transfer under any reading and keeps what its own declaration says.
func TestDeposit_ClaimingResolvesTheRun(t *testing.T) {
	ps := []db.GroupingPosting{
		lumpSum("open", "20000", 10, 971613428),
		spend("spent", "-20000", 10, 971613429),
		landing("landed", "20000", 10, 971613430),
	}
	gs := Partition(ps, allRules(), DefaultOpts())

	if got := resolvedOf(gs, "open"); got != typev1.TxType_TRANSFER {
		t.Fatalf("open resolved to %v, want TRANSFER", got)
	}
	// Declared {TRADE_CASH, TRANSFER}: claiming it as the money that landed is what
	// settles which of the two it was, and what routes the group's residual to
	// TRANSFER_CLEARING rather than IMBALANCE.
	if got := resolvedOf(gs, "landed"); got != typev1.TxType_TRANSFER {
		t.Fatalf("landed resolved to %v, want TRANSFER", got)
	}
	if got := resolvedOf(gs, "spent"); got != typev1.TxType_TRADE_CASH {
		t.Fatalf("spent resolved to %v, want TRADE_CASH", got)
	}
}

// A member another rule took costs the run that member rather than the whole run,
// which is what the smaller variants each opener states are for. The engine drops a
// claim naming a taken posting whole, so without them the run would collapse.
func TestDeposit_FallsBackToAPartialRun(t *testing.T) {
	ps := []db.GroupingPosting{
		lumpSum("open", "20000", 10, 971613428),
		spend("spent", "-20000", 10, 971613429),
		landing("landed", "20000", 10, 971613430),
		// A sale of exactly the landing's amount, settling the same day, which the
		// disposal rule claims before the run is built.
		security("sale", "-100", "200", "20000", 10, 971613431),
	}
	got := members(Partition(ps, allRules(), DefaultOpts()))
	want := [][]string{{"landed", "sale"}, {"open", "spent"}}
	if !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// The nearest row of a direction may be one another rule has taken, and the run
// should then reach past it rather than go without that step. This is why an opener
// states its whole ranked field rather than its best pair.
func TestDeposit_ReachesPastARowAnotherRuleTook(t *testing.T) {
	ps := []db.GroupingPosting{
		lumpSum("open", "20000", 10, 971613428),
		// The nearer spend, which the purchase below claims first.
		spend("tradeCash", "-20000", 10, 971613429),
		security("buy", "100", "200", "20000", 10, 971613427),
		// The run's own spend, further away but still inside the span.
		spend("spent", "-20000", 10, 971613432),
		landing("landed", "20000", 10, 971613433),
	}
	got := members(Partition(ps, allRules(), DefaultOpts()))
	want := [][]string{{"buy", "tradeCash"}, {"landed", "open", "spent"}}
	if !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// Two deposits of the same amount in one account, close enough that both could take
// either spend row. The first opener takes the row beside it and the second reaches
// to the next, rather than proposing a row that has gone and losing its whole run.
//
// This is the state no list fixed before the rule starts could see: the competition
// is between two claims of the same pass, not between rules.
func TestDeposit_SecondOpenerReachesPastTheFirstsRow(t *testing.T) {
	ps := []db.GroupingPosting{
		lumpSum("open1", "20000", 10, 700000100),
		lumpSum("open2", "20000", 10, 700000101),
		spend("spent1", "-20000", 10, 700000102),
		spend("spent2", "-20000", 10, 700000103),
	}
	got := members(Partition(ps, allRules(), DefaultOpts()))
	want := [][]string{{"open1", "spent1"}, {"open2", "spent2"}}
	if !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

func TestDeposit_IsOrderIndependent(t *testing.T) {
	ps := []db.GroupingPosting{
		lumpSum("open1", "19996", 10, 700000100),
		spend("spent1", "-19996", 10, 700000101),
		landing("landed1", "19996", 10, 700000102),
		lumpSum("open2", "19995", 10, 700000103),
		spend("spent2", "-19995", 10, 700000104),
		landing("landed2", "19995", 10, 700000105),
	}
	want := members(Partition(ps, allRules(), DefaultOpts()))
	for i := range len(ps) {
		rotated := make([]db.GroupingPosting, len(ps))
		for j := range rotated {
			rotated[j] = ps[(j+i)%len(ps)]
		}
		if got := members(Partition(rotated, allRules(), DefaultOpts())); !equal(got, want) {
			t.Fatalf("rotation %d: partition = %v, want %v", i, got, want)
		}
	}
}
