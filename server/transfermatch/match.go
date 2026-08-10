// Package transfermatch pairs the two sides of a transfer.
//
// Each side of a journal is balanced against a TRANSFER_CLEARING counterparty and
// the two are deliberately not paired at ingest, because a broker reports them in
// separate statements and sometimes in separate imports. This is the pairing that
// deferral names. It runs after ingestion, over all stored state rather than over
// one job's payload, and it is re-runnable: the second side can arrive in a later
// import than the first.
//
// See docs/adr/0037-transfer-matches-are-links-not-postings.md.
package transfermatch

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/leedenison/portfoliodb/server/db"
)

// matchWindow is how far apart the two sides of one transfer may be dated.
//
// They are dated by their own statements and need not agree: in the sample export
// the deal dates match while the settlement dates differ, and one deposit run
// completes across two days. A same-day window would split pairs the source clearly
// intends.
//
// Bounded above by defaultStaleTransferDays in server/service/api, and deliberately
// no wider: a pair still within the matcher's reach must never be reported as an
// unmatched transfer, which would be the same "the signal carries no information"
// failure this exists to fix, arriving from the other direction.
const matchWindow = 7 * 24 * time.Hour

// refSpan is the widest gap between two sides' broker references that still reads as
// proximity rather than coincidence.
//
// A broker issues the two sides of one transfer together, so their references are
// near: across the two master exports the gap between a real pair runs from 1 to 59,
// with most under 10. DEPOSIT_REF_SPAN in fidelity-csv.ts is 8, but that spans one
// deposit run within one account, which is a tighter thing than two accounts'
// statements of the same movement.
//
// Sixty-four is where the masters stop changing: raising it to 128, 256 or 1024 pairs
// nothing further, and every side left over has no equal-and-opposite counterpart in
// the window at all. That plateau is the real finding -- the date window already
// bounds how far apart two references issued for one movement can be, so this is a
// sanity bound against a coincidence rather than the thing that discriminates. What
// discriminates is that a pair is the *nearest* available and is not tied, which is
// what separates one month's fee transfer from the next when the amounts are equal.
const refSpan = 64

// Opts is what pairing needs beyond the sides themselves. It carries the window
// rather than reading a clock, so Match is a pure function and its tests are not
// time-dependent.
type Opts struct {
	Window  time.Duration
	RefSpan int64
}

// DefaultOpts is the calibrated configuration.
func DefaultOpts() Opts {
	return Opts{Window: matchWindow, RefSpan: refSpan}
}

// candidate is one feasible pairing and how good it is.
type candidate struct {
	from, to int
	// refDistance is the nearest gap between the two sides' references. Only
	// meaningful for a reference match; zero otherwise.
	refDistance int64
	dateGap     time.Duration
}

// betterThan orders candidates within a pass: nearest references first, then nearest
// dates, then group id so a shuffled input yields identical output.
func (c candidate) betterThan(o candidate, sides []db.TransferSide) bool {
	if c.refDistance != o.refDistance {
		return c.refDistance < o.refDistance
	}
	if c.dateGap != o.dateGap {
		return c.dateGap < o.dateGap
	}
	if sides[c.from].GroupID != sides[o.from].GroupID {
		return sides[c.from].GroupID < sides[o.from].GroupID
	}
	return sides[c.to].GroupID < sides[o.to].GroupID
}

// ties reports whether two candidates are indistinguishable on the evidence, ignoring
// the group-id tiebreak that only exists to make the output stable. A side with two
// equally good candidates is left unmatched rather than paired arbitrarily.
func (c candidate) ties(o candidate) bool {
	return c.refDistance == o.refDistance && c.dateGap == o.dateGap
}

// Match pairs the unmatched sides of a transfer and returns the links to record.
//
// Pure and deterministic: the same sides in any order yield the same output. A side
// it cannot pair on evidence is simply absent from the result and stays unmatched,
// which is the point -- a wrong pair looks authoritative, points at the wrong
// account, and stops the report finding the sides that really are missing.
func Match(sides []db.TransferSide, opts Opts) []db.TransferMatch {
	var out []db.TransferMatch
	for _, part := range partition(sides) {
		// The pointer pass first: where the source names the other account it is
		// better evidence than proximity, and consuming those sides first keeps a
		// named pair from being taken by a merely adjacent one.
		taken := map[int]bool{}
		out = append(out, run(sides, part, opts, taken, db.TransferMatchPointer, pointerFeasible)...)
		out = append(out, run(sides, part, opts, taken, db.TransferMatchReference, referenceFeasible)...)
	}
	return out
}

// partition splits the sides into the sets a transfer can be found within. A transfer
// never crosses a user, and never changes commodity: the two sides of a JRNLSEC are
// the same security and the two sides of a cash journal the same currency.
//
// Each partition is returned as a pair of index slices, departures first. A departure
// is a side whose residual is positive, meaning the value left that account, because
// the group's own leg is negative and the clearing leg holds what is owed out. A zero
// residual cannot occur: balancing writes no posting for one.
func partition(sides []db.TransferSide) []partitioned {
	byKey := map[string]*partitioned{}
	var order []string
	for i, s := range sides {
		key := s.UserID + "\x00" + s.InstrumentID
		p, seen := byKey[key]
		if !seen {
			p = &partitioned{}
			byKey[key] = p
			order = append(order, key)
		}
		if s.Amount.IsPositive() {
			p.departures = append(p.departures, i)
		} else if s.Amount.IsNegative() {
			p.arrivals = append(p.arrivals, i)
		}
	}
	out := make([]partitioned, 0, len(order))
	for _, key := range order {
		out = append(out, *byKey[key])
	}
	return out
}

type partitioned struct {
	departures []int
	arrivals   []int
}

// feasible is the test a pass adds on top of the common one. It returns whether the
// pair is a candidate for that pass and, for a reference match, how near the two
// sides' references are.
type feasible func(a, b db.TransferSide, opts Opts) (bool, int64)

// run takes one pass over a partition, consuming sides as it pairs them.
//
// Candidates are ranked globally and taken best-first rather than in row order,
// because taking in order lets one pairing strand another that had no alternative.
// This mirrors how the Fidelity converter ranks buy candidates, for the same reason.
func run(sides []db.TransferSide, part partitioned, opts Opts, taken map[int]bool, method string, extra feasible) []db.TransferMatch {
	var cands []candidate
	for _, from := range part.departures {
		if taken[from] {
			continue
		}
		for _, to := range part.arrivals {
			if taken[to] {
				continue
			}
			if !pairable(sides[from], sides[to], opts) {
				continue
			}
			ok, distance := extra(sides[from], sides[to], opts)
			if !ok {
				continue
			}
			cands = append(cands, candidate{
				from: from, to: to,
				refDistance: distance,
				dateGap:     gap(sides[from].Timestamp, sides[to].Timestamp),
			})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].betterThan(cands[j], sides) })

	// Sides this pass cannot tell apart. Kept separate from taken, which is shared
	// across passes: evidence one pass could not choose on may still decide the
	// pair, so an ambiguous side sits out this pass rather than the whole run.
	blocked := map[int]bool{}
	skip := func(i int) bool { return taken[i] || blocked[i] }

	var out []db.TransferMatch
	for i, c := range cands {
		if skip(c.from) || skip(c.to) {
			continue
		}
		// A side with two equally good candidates still available is not
		// distinguishable, so neither is taken. Two transfers of the same amount
		// between the same accounts in the same window are exactly this case.
		if ambiguous(cands[i+1:], c, skip) {
			blocked[c.from] = true
			blocked[c.to] = true
			continue
		}
		taken[c.from] = true
		taken[c.to] = true
		out = append(out, db.TransferMatch{
			UserID:       sides[c.from].UserID,
			FromGroupID:  sides[c.from].GroupID,
			ToGroupID:    sides[c.to].GroupID,
			InstrumentID: sides[c.from].InstrumentID,
			Method:       method,
		})
	}
	return out
}

// ambiguous reports whether any still-available candidate ties the best one on
// evidence while sharing a side with it. Sorted input means a tie can only be among
// the candidates immediately following.
func ambiguous(rest []candidate, best candidate, skip func(int) bool) bool {
	for _, c := range rest {
		if !c.ties(best) {
			return false
		}
		if skip(c.from) || skip(c.to) {
			continue
		}
		if c.from == best.from || c.to == best.to {
			return true
		}
	}
	return false
}

// pairable is what every pass requires of a pair, whatever evidence names it.
func pairable(a, b db.TransferSide, opts Opts) bool {
	// Exact, with no tolerance. Both figures are exact decimals and both statements
	// quote the same amount; a difference small enough to be the source rounding
	// itself would have been routed to SOURCE_ROUNDING rather than to clearing, so
	// nothing legitimate lands in between.
	if !a.Amount.Add(b.Amount).IsZero() {
		return false
	}
	// A transfer has two accounts. Money moving within one account is not one.
	if a.Broker == b.Broker && strings.EqualFold(a.Account, b.Account) {
		return false
	}
	return gap(a.Timestamp, b.Timestamp) <= opts.Window
}

// pointerFeasible reports whether one side names the other's account outright.
//
// Same broker, because an account name means nothing outside the broker that issued
// it. Either direction: only the receiving side names the counterparty in the sample,
// but nothing guarantees which side a source chooses to write it on.
//
// A pointer still needs the window and the ambiguity test that follow. It names the
// counterparty *account*, not the occurrence, and the sample's monthly fee transfers
// move the same amounts between the same account pair every month -- which a pointer
// plus an amount matches twelve times a year.
// A correlation declaring MATCH_ACCOUNT is what a pointer is: its token names some
// other posting's account rather than a token that posting carries. Reading the
// declaration rather than a field of its own is what lets a source say the same
// thing in whatever field it happens to use.
func pointerFeasible(a, b db.TransferSide, _ Opts) (bool, int64) {
	if a.Broker != b.Broker {
		return false, 0
	}
	return names(a.Correlations, b.Account) || names(b.Correlations, a.Account), 0
}

func names(cs []db.Correlation, account string) bool {
	for _, c := range cs {
		if !declares(c, db.MatchAccount) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(c.Token), strings.TrimSpace(account)) {
			return true
		}
	}
	return false
}

// declares reports whether a correlation offers the given comparison.
func declares(c db.Correlation, match string) bool {
	return slices.Contains(c.Match, match)
}

// referenceFeasible reports whether the two sides' broker references are near enough
// to be the two halves of one transfer, and how near.
//
// Same broker, since two brokers' reference sequences are unrelated and a numeric
// coincidence between them means nothing.
//
// The nearest gap over both sets, not between two chosen values: a group is several
// source rows and the reference nearest the other side is the one that matters. The
// sample's lump sum is three consecutive references against a fourth, where the
// nearest gap is 1 and the widest is 3.
//
// A source whose identifiers are opaque simply makes this pass inapplicable: it
// declares no MATCH_ORDINAL, so nothing here has an ordinal to compare. That is an
// OFX FITID, which is unique within one account and carries a number nothing but the
// institution knows how to take, so two of them are never comparable across accounts
// by distance. Whether a reference carries a number is the converter's to say, since
// it is the only thing that knows its broker's numbering.
func referenceFeasible(a, b db.TransferSide, opts Opts) (bool, int64) {
	if a.Broker != b.Broker {
		return false, 0
	}
	as, bs := ordinals(a.Correlations), ordinals(b.Correlations)
	if len(as) == 0 || len(bs) == 0 {
		return false, 0
	}
	nearest := int64(-1)
	for _, x := range as {
		for _, y := range bs {
			d := x - y
			if d < 0 {
				d = -d
			}
			if nearest < 0 || d < nearest {
				nearest = d
			}
		}
	}
	if nearest > opts.RefSpan {
		return false, 0
	}
	return true, nearest
}

// ordinals collects the numbers the correlations declaring MATCH_ORDINAL carry.
// Nothing is parsed here: the ordinal arrives as a number because the converter put
// one there, and a correlation without one has already said it has none.
func ordinals(cs []db.Correlation) []int64 {
	out := make([]int64, 0, len(cs))
	for _, c := range cs {
		if declares(c, db.MatchOrdinal) && c.Ordinal != nil {
			out = append(out, *c.Ordinal)
		}
	}
	return out
}

func gap(a, b time.Time) time.Duration {
	d := a.Sub(b)
	if d < 0 {
		return -d
	}
	return d
}
