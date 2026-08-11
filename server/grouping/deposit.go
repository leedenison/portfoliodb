package grouping

import (
	"context"
	"sort"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/txtype"
)

// DepositPrecedence puts deposit runs last.
//
// A run is only ever built from money rows the trade rules did not want, which is
// what stops a deposit taking the cash row of a trade of the same amount on the same
// day. The converter proved the ordering against its sample exports and it is
// correctness rather than tuning here for the same reason: a claim is irrevocable,
// so whichever rule goes first decides.
const DepositPrecedence = 600

// Deposit groups the run of money rows a deposit into a product account is reported
// through.
//
// Money paid into a wrapper arrives as a run of rows of one amount: the subscription
// is credited, spent, and credited again as the money that lands. No security is
// involved, and the three are one event. Left ungrouped they are separate
// single-posting groups, two of them transfers, so the account reports twice the
// contribution it received and a residual the size of the deposit lands in IMBALANCE.
func Deposit() Rule { return deposit{} }

type deposit struct{}

func (deposit) Name() string { return "deposit_run" }

func (deposit) Precedence() int { return DepositPrecedence }

// Expand asks for the accounts' postings whose references fall within the span of
// these, in both directions -- a posting can be the row that opens a run or a row
// some earlier reference opened.
//
// The span is the source's own, carried on the correlation, because how densely a
// broker issues references is a fact about its numbering rather than a grouping
// policy. A server-side constant would be wrong for every broker but one.
//
// No date bound. A run in the sample exports settles across two days -- the lump sum
// and its spend complete on the 11th and the arrival on the 14th -- so a date bucket
// would split it.
func (deposit) Expand(ctx context.Context, userID string, ps []db.GroupingPosting, r db.GroupingReader, held []string) ([]db.GroupingPosting, error) {
	var qs []db.OrdinalQuery
	for _, p := range ps {
		for _, c := range p.Correlations {
			if !c.Declares(db.MatchOrdinal) || c.Ordinal == nil || c.OrdinalSpan == nil {
				continue
			}
			qs = append(qs, db.OrdinalQuery{
				Broker:  p.Broker,
				Account: p.Account,
				Label:   c.Label,
				Low:     *c.Ordinal - *c.OrdinalSpan,
				High:    *c.Ordinal + *c.OrdinalSpan,
			})
		}
	}
	if len(qs) == 0 {
		return nil, nil
	}
	return r.PostingsByOrdinals(ctx, userID, distinct(qs), held)
}

// Apply builds a run from each row that could open one.
//
// What separates one deposit from another is the account, the amount agreeing to the
// penny, and the trade rules having already taken the cash rows they need. The span
// is a guard against a distant coincidence rather than the thing that discriminates:
// every span from the widest real run upward groups the same runs.
//
// Openers are walked in reference order rather than interleaved by quality, which is
// how the converter walks them: an earlier run takes the rows it needs and a later
// one builds from what is left. Each takes the nearest row of each direction still
// free, so a row another rule claimed costs the run that row rather than the step,
// and the run reaches past it.
func (d deposit) Apply(ps []db.GroupingPosting, st *State, opts Opts) {
	var openers, members []db.GroupingPosting
	for _, p := range ps {
		if st.Taken(p.ID) || !isMoney(p) || p.Quantity.IsZero() {
			continue
		}
		// The row that opens a run is money arriving that the source says is a
		// transfer under every reading. That declaration is also what routes the
		// finished group's residual to TRANSFER_CLEARING, where the departure it
		// answers is waiting.
		if p.Quantity.IsPositive() && txtype.MustBe(p.Declared, typev1.TxType_TRANSFER) {
			openers = append(openers, p)
			// A row that opens a run is not a step in someone else's. The source
			// reports a run as a subscription credited, spent and credited again,
			// and it is the middle two that carry a trade's cash leg in their
			// declaration; a second arrival that says only "transfer" is a second
			// deposit rather than more of this one.
			continue
		}
		if txtype.MayBe(p.Declared, typev1.TxType_TRADE_CASH) || txtype.MayBe(p.Declared, typev1.TxType_TRANSFER) {
			members = append(members, p)
		}
	}
	sort.Slice(openers, func(i, j int) bool { return byOrdinal(openers[i], openers[j]) })

	for _, o := range openers {
		if st.Taken(o.ID) {
			continue
		}
		ms := []Member{{ID: o.ID, Resolved: typev1.TxType_TRANSFER}}
		if m, ok := d.nearest(o, members, st, opts, true); ok {
			ms = append(ms, member(m))
		}
		if m, ok := d.nearest(o, members, st, opts, false); ok {
			ms = append(ms, member(m))
		}
		// A lump sum with no run at all is money paid in from outside and stands on
		// its own, which is what Claim refusing a lone member already says.
		st.Claim(ms...)
	}
}

// nearest returns the closest still-free money row this opener could have moved in
// one direction: the subscription being spent, or the money landing.
//
// One row per direction, never two, because the source reports a run as one row per
// step and a second row of the same amount and direction inside the span is a
// different deposit rather than more of this one.
//
// Nearest wins, which is what keeps two runs of nearly equal amount in one account
// apart: each takes the rows issued beside it. A row another rule has taken, or one
// an earlier opener of this same pass took, is passed over rather than ending the
// search -- so a run whose closest step went elsewhere reaches to the next rather
// than losing the step.
func (d deposit) nearest(o db.GroupingPosting, members []db.GroupingPosting, st *State, opts Opts, spending bool) (db.GroupingPosting, bool) {
	span, ok := ordinalSpan(o)
	if !ok {
		return db.GroupingPosting{}, false
	}
	var best db.GroupingPosting
	found := false
	for _, m := range members {
		if st.Taken(m.ID) || m.ID == o.ID {
			continue
		}
		if m.Account != o.Account || m.Broker != o.Broker || m.Quantity.IsNegative() != spending {
			continue
		}
		gap, ok := forwardGap(o, m)
		// Strictly ascending: the run climbs away from the row that opened it, so a
		// lower reference belongs to something earlier rather than to this.
		if !ok || gap <= 0 || gap > span {
			continue
		}
		if m.Quantity.Abs().Sub(o.Quantity.Abs()).Abs().GreaterThanOrEqual(opts.Money) {
			continue
		}
		if !found || byOrdinal(m, best) {
			best, found = m, true
		}
	}
	return best, found
}

// member resolves one row of a run.
//
// The rule that claims a row is what resolves it, and this rule claims a movement of
// money into an account. So a row whose declared set admits a transfer is settled as
// one -- which is what narrows Fidelity's Cash In, reported the same way whether
// money arrived from elsewhere or a switch's sale settled. A row that cannot be a
// transfer is the subscription being spent inside the account, and keeps what its own
// declaration says.
func member(m db.GroupingPosting) Member {
	if txtype.MayBe(m.Declared, typev1.TxType_TRANSFER) {
		return Member{ID: m.ID, Resolved: typev1.TxType_TRANSFER}
	}
	return Member{ID: m.ID, Resolved: txtype.Resolve(m.Declared)}
}

// ordinalSpan returns how far apart two references may be and still be about one
// event, as the source states it on this posting.
func ordinalSpan(p db.GroupingPosting) (int64, bool) {
	for _, c := range p.Correlations {
		if c.Declares(db.MatchOrdinal) && c.Ordinal != nil && c.OrdinalSpan != nil {
			return *c.OrdinalSpan, true
		}
	}
	return 0, false
}

// forwardGap returns how far b's reference sits after a's, in the series both carry.
// Directed, unlike the distance the trade rules rank on: a run ascends, so which
// side a reference falls on is the evidence.
func forwardGap(a, b db.GroupingPosting) (int64, bool) {
	for _, ca := range a.Correlations {
		if !ca.Declares(db.MatchOrdinal) || ca.Ordinal == nil {
			continue
		}
		for _, cb := range b.Correlations {
			if cb.Label != ca.Label || !cb.Declares(db.MatchOrdinal) || cb.Ordinal == nil {
				continue
			}
			return *cb.Ordinal - *ca.Ordinal, true
		}
	}
	return 0, false
}

// byOrdinal orders two postings by the reference they carry, falling back to the id
// so the order is total and a shuffled input yields identical output.
func byOrdinal(a, b db.GroupingPosting) bool {
	oa, aok := firstOrdinal(a)
	ob, bok := firstOrdinal(b)
	if aok && bok && oa != ob {
		return oa < ob
	}
	if aok != bok {
		return aok
	}
	return a.ID < b.ID
}

func firstOrdinal(p db.GroupingPosting) (int64, bool) {
	for _, c := range p.Correlations {
		if c.Declares(db.MatchOrdinal) && c.Ordinal != nil {
			return *c.Ordinal, true
		}
	}
	return 0, false
}
