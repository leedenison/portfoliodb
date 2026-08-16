package grouping

import (
	"math/rand"
	"testing"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// The evidence this rule exists for: the source states 7390.19 for the purchase and
// -7380.19 for the cash that settled it, and the 10.00 between them is the dealing
// fee. Two figures the source wrote rather than a proximity.
func TestCharge_ExplainsAnAcquisitionsGap(t *testing.T) {
	asset := security("a", "28", "263.58", "7390.19", 10, 563466632)
	cash := money("b", "-7380.19", 10, 563466631, typev1.TxType_TRADE_CASH)
	fee := charge("c", "-10", 10, 167614596)

	got := members(Partition([]db.GroupingPosting{asset, cash, fee}, DefaultRules(), DefaultOpts()))
	if want := [][]string{{"a", "b", "c"}}; !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// A UK purchase carries a dealing fee, a levy and stamp duty, and the gap is their
// sum. Nothing says which of the three it is, and nothing needs to: the group is the
// same either way.
func TestCharge_SumsSeveralChargesAgainstOneGap(t *testing.T) {
	asset := security("a", "28", "263.58", "7399.00", 10, 563466632)
	cash := money("b", "-7380.19", 10, 563466631, typev1.TxType_TRADE_CASH)
	fee := charge("c", "-7.50", 10, 167614596)
	levy := charge("d", "-1.50", 10, 167614597)
	duty := charge("e", "-9.81", 10, 167614598)

	got := members(Partition([]db.GroupingPosting{asset, cash, fee, levy, duty}, DefaultRules(), DefaultOpts()))
	if want := [][]string{{"a", "b", "c", "d", "e"}}; !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// A disposal states its gross proceeds, so its total equals its cash in exactly and
// there is nothing left for a charge to explain. The rule must not read that zero as
// licence to take whatever charge is nearby.
func TestCharge_LeavesADisposalsChargeAlone(t *testing.T) {
	asset := security("a", "-1242", "5.85", "7266.49", 10, 563466569)
	cash := money("b", "7266.49", 10, 563466571, typev1.TxType_TRADE_CASH)
	fee := charge("c", "-7.50", 10, 167614596)

	got := members(Partition([]db.GroupingPosting{asset, cash, fee}, DefaultRules(), DefaultOpts()))
	if want := [][]string{{"a", "b"}, {"c"}}; !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// The netting that stops this rule fighting the one above it. A buy's gap is a
// dealing fee plus a stamp duty; once a stated pointer has claimed the fee, the
// whole gap is no longer a subset of what is left, and without netting off what the
// group already holds this rule would explain nothing.
func TestCharge_NetsOffWhatIsAlreadyInTheGroup(t *testing.T) {
	asset := security("a", "28", "263.58", "7399.00", 10, 563466632)
	cash := money("b", "-7380.19", 10, 563466631, typev1.TxType_TRADE_CASH)
	// The converter named this one's trade, so Attaches claims it first.
	fee := pointsAt(charge("c", "-7.50", 8, 167614596), "", "563466632", db.ScopeFile)
	// This one it could not, and 7399.00 - 7380.19 - 7.50 = 11.31 is what is left.
	duty := charge("d", "-11.31", 10, 167614597)

	got := members(Partition([]db.GroupingPosting{asset, cash, fee, duty}, DefaultRules(), DefaultOpts()))
	if want := [][]string{{"a", "b", "c", "d"}}; !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// A charge in another account, or on another order date, is not in the bucket
// whatever its amount.
func TestCharge_DoesNotReachOutsideTheBucket(t *testing.T) {
	asset := security("a", "28", "263.58", "7390.19", 10, 563466632)
	cash := money("b", "-7380.19", 10, 563466631, typev1.TxType_TRADE_CASH)
	otherDay := charge("c", "-10", 11, 167614596)
	otherAccount := charge("d", "-10", 10, 167614597)
	otherAccount.Account = "A2"

	got := members(Partition([]db.GroupingPosting{asset, cash, otherDay, otherAccount}, DefaultRules(), DefaultOpts()))
	if want := [][]string{{"a", "b"}, {"c"}, {"d"}}; !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// Nothing in the bucket adds up to the gap, so nothing is attached. A rule that
// took the nearest charge instead would be asserting an occurrence on proximity.
func TestCharge_LeavesAGapNothingExplains(t *testing.T) {
	asset := security("a", "28", "263.58", "7390.19", 10, 563466632)
	cash := money("b", "-7380.19", 10, 563466631, typev1.TxType_TRADE_CASH)
	fee := charge("c", "-3.33", 10, 167614596)

	got := members(Partition([]db.GroupingPosting{asset, cash, fee}, DefaultRules(), DefaultOpts()))
	if want := [][]string{{"a", "b"}, {"c"}}; !equal(got, want) {
		t.Fatalf("partition = %v, want %v", got, want)
	}
}

// A posting whose declared set left open whether it was a charge is one this rule
// would be asserting something about. An amount that happens to fit is not enough to
// settle a question the source left open.
func TestCharge_TakesOnlyWhatMustBeACharge(t *testing.T) {
	asset := security("a", "28", "263.58", "7390.19", 10, 563466632)
	cash := money("b", "-7380.19", 10, 563466631, typev1.TxType_TRADE_CASH)
	maybe := money("c", "-10", 10, 167614596, typev1.TxType_TRADE_CASH, typev1.TxType_TRANSFER)

	got := members(Partition([]db.GroupingPosting{asset, cash, maybe}, DefaultRules(), DefaultOpts()))
	for _, g := range got {
		if len(g) == 3 {
			t.Fatalf("claimed a posting that need not be a charge: %v", got)
		}
	}
}

// Two purchases on one day can both be explained by a 7.50 fee, so the rule ranks
// every candidate before taking any. Whichever it takes, it must take one and leave
// the other rather than giving the fee to both or losing it entirely.
func TestCharge_DoesNotGiveOneChargeToTwoTrades(t *testing.T) {
	a := security("a", "28", "263.58", "7387.69", 10, 563466632)
	b := money("b", "-7380.19", 10, 563466631, typev1.TxType_TRADE_CASH)
	d := security("d", "60", "80.00", "4807.50", 10, 563466700)
	e := money("e", "-4800.00", 10, 563466701, typev1.TxType_TRADE_CASH)
	fee := charge("c", "-7.50", 10, 167614596)

	gs := Partition([]db.GroupingPosting{a, b, fee, d, e}, DefaultRules(), DefaultOpts())
	seen := 0
	for _, g := range gs {
		for _, m := range g.Members {
			if m.ID == "c" {
				seen++
				if len(g.Members) != 3 {
					t.Fatalf("charge landed in a group of %d: %v", len(g.Members), members(gs))
				}
			}
		}
	}
	if seen != 1 {
		t.Fatalf("charge appears in %d groups, want 1: %v", seen, members(gs))
	}
}

// The engine is handed a neighbourhood rather than a stream, and a subset search is
// where an order dependence would hide.
func TestCharge_IsOrderIndependent(t *testing.T) {
	ps := []db.GroupingPosting{
		security("a", "28", "263.58", "7399.00", 10, 563466632),
		money("b", "-7380.19", 10, 563466631, typev1.TxType_TRADE_CASH),
		charge("c", "-7.50", 10, 167614596),
		charge("d", "-1.50", 10, 167614597),
		charge("e", "-9.81", 10, 167614598),
		security("f", "60", "80.00", "4807.50", 10, 563466700),
		money("g", "-4800.00", 10, 563466701, typev1.TxType_TRADE_CASH),
	}
	want := members(Partition(ps, DefaultRules(), DefaultOpts()))

	r := rand.New(rand.NewSource(11))
	for i := range 20 {
		shuffled := make([]db.GroupingPosting, len(ps))
		copy(shuffled, ps)
		r.Shuffle(len(shuffled), func(x, y int) { shuffled[x], shuffled[y] = shuffled[y], shuffled[x] })
		if got := members(Partition(shuffled, DefaultRules(), DefaultOpts())); !equal(got, want) {
			t.Fatalf("shuffle %d: partition = %v, want %v", i, got, want)
		}
	}
}
