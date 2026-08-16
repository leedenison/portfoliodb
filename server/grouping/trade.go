package grouping

import (
	"context"
	"math"
	"sort"
	"time"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/txtype"
)

// The trade rules in the order they claim.
//
// Disposals go before acquisitions because a disposal's test is equality and an
// acquisition's is an inequality: the source states the same total on a sale and its
// cash in, while a purchase's total carries charges the cash row does not. The
// weaker test must not take a row the stronger one can identify outright.
//
// Both go before the cash-for-cash rule, which reproduces the converter's ordering
// of security trades ahead of trades of the account's own cash: nothing in the
// sample exports needs it -- no bucket holds a cash and a security trade of equal
// amount -- but five hold both kinds, so this keeps which one wins from depending on
// the order a broker happened to export them in.
const (
	DisposalPrecedence    = 900
	AcquisitionPrecedence = 800
	CashTradePrecedence   = 700
)

// Disposal pairs a sold asset with the money that came in for it.
func Disposal() Rule {
	return trade{name: "disposal", precedence: DisposalPrecedence}
}

// Acquisition pairs a bought asset with the money that went out for it.
func Acquisition() Rule {
	return trade{name: "acquisition", precedence: AcquisitionPrecedence, acquiring: true}
}

// trade pairs an asset leg with the cash leg that settles it.
//
// The broker row type that named which cash row settles which trade -- Fidelity
// calls a sale's cash in a "Cash In From Sell" and a purchase's cash out a "Cash Out
// For Buy" -- does not survive into the standard format, and does not need to. What
// it encoded is the direction the money moved, which is the sign of the cash
// posting's own quantity, and which kind of trade it settles, which the declared set
// already distinguishes. What it encoded beyond that narrows candidates without
// identifying an occurrence, and the ranking below does that better.
type trade struct {
	name       string
	precedence int
	// acquiring says which way the asset moved: an acquisition takes money out and
	// a disposal brings it in.
	acquiring bool
}

func (t trade) Name() string { return t.name }

func (t trade) Precedence() int { return t.precedence }

// Expand asks for the accounts' postings on the same days.
//
// The trade rules pair within one account on one day, because both legs of a trade
// settle together -- unlike a deposit run, which the source can spread across two.
func (t trade) Expand(ctx context.Context, userID string, ps []db.GroupingPosting, r db.GroupingReader, held []string) ([]db.GroupingPosting, error) {
	return expandByDay(ctx, userID, ps, r, held)
}

// expandByDay is the read every rule that buckets on a settlement day makes. Shared
// so the three of them cannot disagree about where a day starts.
func expandByDay(ctx context.Context, userID string, ps []db.GroupingPosting, r db.GroupingReader, held []string) ([]db.GroupingPosting, error) {
	qs := make([]db.DateQuery, 0, len(ps))
	for _, p := range ps {
		y, m, d := p.OrderDate.UTC().Date()
		day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		qs = append(qs, db.DateQuery{
			Broker:  p.Broker,
			Account: p.Account,
			From:    day,
			Before:  day.AddDate(0, 0, 1),
		})
	}
	if len(qs) == 0 {
		return nil, nil
	}
	return r.PostingsByDates(ctx, userID, distinct(qs), held)
}

// Apply pairs each asset leg with a cash leg, ranked globally.
//
// Ranking is across the whole neighbourhood before any claim is made, so that one
// trade cannot strand another by taking its cash row. The converter learned this on
// its buy pass and its sell pass does without, taking the first candidate in row
// order; doing it for both is strictly better evidence and costs nothing.
func (t trade) Apply(ps []db.GroupingPosting, st *State, opts Opts) {
	var assets, cash []db.GroupingPosting
	for _, p := range ps {
		if st.Taken(p.ID) {
			continue
		}
		switch {
		case t.isAssetLeg(p):
			assets = append(assets, p)
		case t.isCashLeg(p):
			cash = append(cash, p)
		}
	}

	var cands []candidate
	for _, a := range assets {
		for _, c := range cash {
			if bucket(a) != bucket(c) {
				continue
			}
			if !t.amountsAgree(a, c, opts) {
				continue
			}
			if !consistent(a, c, opts) {
				continue
			}
			cands = append(cands, candidate{
				assetID:  a.ID,
				cashID:   c.ID,
				distance: ordinalDistance(a, c),
			})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].betterThan(cands[j]) })

	// Best first, skipping whatever an earlier claim of this same pass has taken.
	// Claim refuses one naming a posting that has gone, so the skip is the whole of
	// the bookkeeping.
	for _, c := range cands {
		st.Claim(
			Member{ID: c.assetID, Resolved: typev1.TxType_TRADE_ASSET},
			Member{ID: c.cashID, Resolved: typev1.TxType_TRADE_CASH},
		)
	}
}

// isAssetLeg reports whether this posting could be the asset side of the trade this
// rule claims: a commodity that is not money, moving in this rule's direction, whose
// declared set admits being a trade's asset leg.
//
// The admissibility test is 0044's *may be* predicate doing real work: it is what
// stops this rule claiming a row whose source said it was definitely a transfer.
func (t trade) isAssetLeg(p db.GroupingPosting) bool {
	if isMoney(p) || p.SettlementAmount == nil {
		return false
	}
	if !txtype.MayBe(p.Declared, typev1.TxType_TRADE_ASSET) {
		return false
	}
	return p.Quantity.IsPositive() == t.acquiring && !p.Quantity.IsZero()
}

// isCashLeg reports whether this posting could be the money that settles the trade:
// money moving opposite to the asset, admitting a trade's cash leg.
func (t trade) isCashLeg(p db.GroupingPosting) bool {
	if !isMoney(p) {
		return false
	}
	if !txtype.MayBe(p.Declared, typev1.TxType_TRADE_CASH) {
		return false
	}
	return p.Quantity.IsNegative() == t.acquiring && !p.Quantity.IsZero()
}

// amountsAgree applies the test that separates the two directions.
//
// A sale's cash in equals the proceeds the source stated for the sale, so equality
// identifies it. A purchase's cash out is the consideration while the source's total
// for the purchase carries the charges as well, so the two differ by a fee -- and a
// fee cannot be negative, which is what rules out the cash row of a larger trade in
// the same bucket. The inequality is the whole of the test in that direction: it is
// weaker than equality, which is why the disposal rule claims first.
func (t trade) amountsAgree(asset, cash db.GroupingPosting, opts Opts) bool {
	stated := asset.SettlementAmount.Abs()
	paid := cash.Quantity.Abs()
	if t.acquiring {
		return stated.Sub(paid).GreaterThan(opts.Money.Neg())
	}
	return stated.Sub(paid).Abs().LessThan(opts.Money)
}

// consistent cross-checks a pairing against the trade's own quantity and price.
//
// Both amount tests above match one figure the source stated against another, so
// neither notices a cash row that is internally consistent but belongs to a
// different trade. quantity * unit price comes from different fields, which makes it
// an independent check: a swapped cash row misses it by percentage points, while a
// fee gap is around 0.02% of a trade and sits far inside the band. So this cannot
// replace the fee rule and the fee rule cannot replace this.
//
// A relative test, so it divides, which puts it past the exactness boundary by
// construction -- a band this wide would not notice the difference anyway. See
// docs/adr/0026-exact-decimals-bounded-by-closure.md.
func consistent(asset, cash db.GroupingPosting, opts Opts) bool {
	if asset.UnitPrice == nil {
		// Nothing quoted to check against. Absence is not evidence of a bad
		// pairing, so the amount test stands on its own.
		return true
	}
	expected := asset.Quantity.Mul(*asset.UnitPrice).Abs()
	if expected.IsZero() {
		return true
	}
	return cash.Quantity.Abs().Sub(expected).Abs().Div(expected).LessThanOrEqual(opts.ConsiderationBand)
}

// CashTrade pairs the two halves of a movement of the account's own cash.
//
// Fidelity models an account's cash as a tradable asset, so money leaving is
// reported as a sale of cash and money arriving as a purchase of it, each settling
// against a cash row beside it. Both postings are money by the time they reach here
// and neither can be a trade's asset leg, so the rules above pass over them: what
// identifies the pair instead is that the two are equal and opposite in one account
// on one day, which is a stronger statement than either of those rules gets to make.
func CashTrade() Rule { return cashTrade{} }

type cashTrade struct{}

func (cashTrade) Name() string { return "cash_trade" }

func (cashTrade) Precedence() int { return CashTradePrecedence }

func (cashTrade) Expand(ctx context.Context, userID string, ps []db.GroupingPosting, r db.GroupingReader, held []string) ([]db.GroupingPosting, error) {
	return expandByDay(ctx, userID, ps, r, held)
}

func (cashTrade) Apply(ps []db.GroupingPosting, st *State, _ Opts) {
	var out, in []db.GroupingPosting
	for _, p := range ps {
		if st.Taken(p.ID) || !isMoney(p) || p.Quantity.IsZero() {
			continue
		}
		// Both sides must be a trade's cash leg under every reading. A row that
		// might be a transfer is not one of these: the money that actually moved
		// stands on its own and the deposit rules are what claim it.
		if !txtype.MustBe(p.Declared, typev1.TxType_TRADE_CASH) {
			continue
		}
		if p.Quantity.IsNegative() {
			out = append(out, p)
		} else {
			in = append(in, p)
		}
	}

	var cands []candidate
	for _, a := range out {
		for _, b := range in {
			if bucket(a) != bucket(b) {
				continue
			}
			// Exact, with no tolerance: these are two statements of one movement
			// of money rather than a total and a price-derived figure, so there is
			// nothing for a rounding to creep into.
			if !a.Quantity.Add(b.Quantity).IsZero() {
				continue
			}
			cands = append(cands, candidate{assetID: a.ID, cashID: b.ID, distance: ordinalDistance(a, b)})
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].betterThan(cands[j]) })

	for _, c := range cands {
		st.Claim(
			Member{ID: c.assetID, Resolved: typev1.TxType_TRADE_CASH},
			Member{ID: c.cashID, Resolved: typev1.TxType_TRADE_CASH},
		)
	}
}

// candidate is one feasible pairing and how good it is.
type candidate struct {
	assetID  string
	cashID   string
	distance int64
}

// betterThan orders candidates within a rule: nearest references first, then the
// posting ids, so a shuffled input yields identical output.
func (c candidate) betterThan(o candidate) bool {
	if c.distance != o.distance {
		return c.distance < o.distance
	}
	if c.assetID != o.assetID {
		return c.assetID < o.assetID
	}
	return c.cashID < o.cashID
}

// noOrdinal ranks a pairing with nothing to compare behind every pairing that has
// something. A source with no numbering is not thereby a worse match, but a pairing
// backed by proximity is a better one, and the ids below still make the order total.
const noOrdinal = int64(math.MaxInt64)

// ordinalDistance is the nearest gap between the two postings' reference numbers,
// over the series both carry.
//
// Nothing is parsed: the ordinal arrives as a number because the converter put one
// there, and a source whose identifiers are opaque declares no ordinal rather than
// being discovered to have none. Compared within a label, so an order id is never
// measured against a settlement reference.
func ordinalDistance(a, b db.GroupingPosting) int64 {
	best := noOrdinal
	for _, ca := range a.Correlations {
		if !ca.Declares(db.MatchOrdinal) || ca.Ordinal == nil {
			continue
		}
		for _, cb := range b.Correlations {
			if cb.Label != ca.Label || !cb.Declares(db.MatchOrdinal) || cb.Ordinal == nil {
				continue
			}
			d := *ca.Ordinal - *cb.Ordinal
			if d < 0 {
				d = -d
			}
			if d < best {
				best = d
			}
		}
	}
	return best
}

// isMoney reports whether this posting's quantity is an amount of money rather than
// a count of something.
//
// A posting is money exactly when it cannot be a trade's asset leg, which is the
// predicate the converters apply to decide whether to read a row's amount as its
// quantity. It is derived rather than carried because a second field saying the same
// thing could disagree with the declared set.
func isMoney(p db.GroupingPosting) bool {
	return !txtype.MayBe(p.Declared, typev1.TxType_TRADE_ASSET)
}

// bucket is the account and day two legs of a trade must share.
//
// The day is the posting's own, in UTC as stored. The converter buckets on the date
// string the source wrote, which is the same statement made before parsing: both
// legs of a trade carry it identically, and pairing only needs them to agree.
func bucket(p db.GroupingPosting) string {
	return p.Account + "\x00" + p.OrderDate.UTC().Format("2006-01-02")
}
