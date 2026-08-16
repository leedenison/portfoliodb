package grouping

import (
	"context"
	"sort"

	"github.com/shopspring/decimal"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/txtype"
)

// ChargePrecedence is below Attaches, so a pointer a source stated always beats an
// amount this rule inferred. Both run below the rules that decide the partition,
// because both only ever add to one.
const ChargePrecedence = 400

// maxChargeSubset is how many charges may be summed against one gap.
//
// Three covers every purchase in the sample exports -- a dealing fee, a levy and one
// of stamp duty or an FX charge -- and the bound is what keeps the search from
// enumerating the power set of a busy day. A gap needing four is a gap this rule
// declines to explain rather than one it explains slowly.
const maxChargeSubset = 3

// Charge attaches a per-trade charge to an acquisition whose own figures account for
// it.
//
// A source that states both a purchase's total and the cash that settled it has
// stated the charges too, as the difference: Fidelity writes 7390.19 against a cash
// row of -7380.19, and the 10.00 is the dealing fee. That difference is a figure the
// source stated twice over rather than a proximity, which is what makes it evidence
// of the occurrence and not merely of the day.
//
// A disposal offers nothing. Its stated total equals its cash in exactly -- the
// broker reports gross proceeds and takes the charge separately -- so the gap is zero
// and there is nothing to explain. That is a fact about the source rather than a
// limitation here, and the rule does not guess at one.
//
// This is the fee-direction inequality
// docs/adr/0047-grouping-runs-as-precedence-ordered-passes.md anticipated, and the
// other half of the acquisition rule's own test: that rule pairs on
// stated >= paid because a charge cannot be negative, and this one accounts for what
// the inequality left over.
type Charge struct{}

func (Charge) Name() string { return "charge" }

func (Charge) Precedence() int { return ChargePrecedence }

// Expand asks for the accounts' postings on the same days, which after
// docs/adr/0051-a-posting-carries-an-order-date-and-a-trade-date.md is the order
// date -- the one a trade and the charges levied on it agree about. The same read
// the trade rules make, and the reason this rule needs no access path of its own.
func (Charge) Expand(ctx context.Context, userID string, ps []db.GroupingPosting, r db.GroupingReader, held []string) ([]db.GroupingPosting, error) {
	return expandByDay(ctx, userID, ps, r, held)
}

// Apply explains each acquisition's gap with the charges of its own bucket.
//
// Ranked across the whole neighbourhood before anything is attached, as every rule
// ranks: two purchases on one day can both be explained by a 7.50 fee, and taking
// them in the order they were read would let one strand the other.
func (Charge) Apply(ps []db.GroupingPosting, st *State, opts Opts) {
	byGroup := map[string][]db.GroupingPosting{}
	for _, p := range ps {
		byGroup[st.Group(p.ID)] = append(byGroup[st.Group(p.ID)], p)
	}

	// The charges still free, by the bucket they could be explained in.
	free := map[string][]db.GroupingPosting{}
	for _, p := range ps {
		if st.Taken(p.ID) || !isCharge(p) {
			continue
		}
		free[bucket(p)] = append(free[bucket(p)], p)
	}
	for _, cs := range free {
		sort.Slice(cs, func(i, j int) bool { return cs[i].ID < cs[j].ID })
	}

	var cands []chargeCandidate
	for _, p := range ps {
		gap, ok := unexplained(p, byGroup[st.Group(p.ID)])
		if !ok {
			continue
		}
		ids, found := subsetSumming(free[bucket(p)], gap, opts.Money)
		if !found {
			continue
		}
		cands = append(cands, chargeCandidate{anchorID: p.ID, chargeIDs: ids})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].betterThan(cands[j]) })

	for _, c := range cands {
		ms := make([]Member, 0, len(c.chargeIDs))
		for _, id := range c.chargeIDs {
			// The rule states which event this charge is a leg of, and nothing
			// about what kind of leg, so it keeps what its own declaration
			// narrows to -- as deposit.member does.
			ms = append(ms, Member{ID: id, Resolved: resolvedOfPosting(ps, id)})
		}
		st.Attach(c.anchorID, ms...)
	}
}

// unexplained returns what an acquisition's own figures leave unaccounted for, and
// whether it is an acquisition with anything left.
//
// The source's stated total for the purchase, less the cash that settled it, less
// whatever charges the group already holds. That last term is what stops this rule
// fighting the one above it: a buy's gap is typically a dealing fee plus a stamp
// duty, so once a stated pointer has claimed the fee, the *whole* gap is no longer
// a subset of what is left and the rule would explain nothing. Netting off what is
// already in the group leaves exactly the part still unaccounted for.
func unexplained(asset db.GroupingPosting, group []db.GroupingPosting) (decimal.Decimal, bool) {
	if isMoney(asset) || asset.SettlementAmount == nil {
		return decimal.Zero, false
	}
	// An acquisition: the asset came in, so money went out and the total the
	// source stated is what it cost.
	if !asset.Quantity.IsPositive() {
		return decimal.Zero, false
	}
	gap := asset.SettlementAmount.Abs()
	for _, m := range group {
		switch {
		case m.ID == asset.ID:
		case isCharge(m):
			gap = gap.Sub(m.Quantity.Abs())
		case isMoney(m) && txtype.MayBe(m.Declared, typev1.TxType_TRADE_CASH):
			gap = gap.Sub(m.Quantity.Abs())
		}
	}
	// Nothing left to explain, or the group already over-explains it. Either way
	// there is no charge this rule can identify.
	return gap, gap.IsPositive()
}

// isCharge reports whether a posting is money the source levied as a cost.
//
// Must-be rather than may-be: a posting whose declared set left open whether it was
// a charge is one this rule would be asserting something about, and an amount that
// happens to fit is not enough to settle a question the source left open.
func isCharge(p db.GroupingPosting) bool {
	return isMoney(p) && !p.Quantity.IsZero() &&
		txtype.MustBe(p.Declared, typev1.TxType_EXPENSE)
}

// subsetSumming finds charges summing to gap, smallest subset first, and reports
// whether it found any.
//
// Smallest first because a single charge explaining a gap exactly is stronger than
// three that happen to add up to it, and within a size the lowest ids, so the answer
// does not depend on the order the postings arrived in. Bounded at maxChargeSubset.
func subsetSumming(charges []db.GroupingPosting, gap, tol decimal.Decimal) ([]string, bool) {
	var pick func(start int, left decimal.Decimal, depth int, acc []string) ([]string, bool)
	pick = func(start int, left decimal.Decimal, depth int, acc []string) ([]string, bool) {
		if left.Abs().LessThan(tol) && len(acc) > 0 {
			out := make([]string, len(acc))
			copy(out, acc)
			return out, true
		}
		if depth == 0 {
			return nil, false
		}
		for i := start; i < len(charges); i++ {
			amount := charges[i].Quantity.Abs()
			// A charge larger than what is left cannot be part of the answer:
			// every charge is positive, so nothing after it can bring the
			// running total back down.
			if amount.Sub(left).GreaterThan(tol) {
				continue
			}
			if got, ok := pick(i+1, left.Sub(amount), depth-1, append(acc, charges[i].ID)); ok {
				return got, true
			}
		}
		return nil, false
	}
	for size := 1; size <= maxChargeSubset; size++ {
		if got, ok := pick(0, gap, size, nil); ok {
			return got, true
		}
	}
	return nil, false
}

// resolvedOfPosting is what a posting's own declaration narrows to, for a rule that
// concluded where it belongs without concluding what it is.
func resolvedOfPosting(ps []db.GroupingPosting, id string) typev1.TxType {
	for _, p := range ps {
		if p.ID == id {
			return txtype.Resolve(p.Declared)
		}
	}
	return typev1.TxType_TX_TYPE_UNSPECIFIED
}

// chargeCandidate is one acquisition and the charges that would explain its gap.
type chargeCandidate struct {
	anchorID  string
	chargeIDs []string
}

// betterThan prefers the smaller explanation, then orders by id so a shuffled input
// yields identical output.
func (c chargeCandidate) betterThan(o chargeCandidate) bool {
	if len(c.chargeIDs) != len(o.chargeIDs) {
		return len(c.chargeIDs) < len(o.chargeIDs)
	}
	return c.anchorID < o.anchorID
}
