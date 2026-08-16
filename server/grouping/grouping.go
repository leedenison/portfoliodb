// Package grouping decides which postings are legs of one economic event.
//
// It recomputes rather than repairs: given every posting of a neighbourhood it
// partitions all of them from scratch in one traversal, and the caller writes only
// where the answer differs from what is stored. Nothing is disturbed because
// nothing is preserved, which is what gives the pass a termination argument that
// incremental repair lacks. See docs/adr/0050-grouping-recomputes-a-neighbourhood.md.
//
// Rules run in a fixed precedence order, a rule may claim a posting only where its
// declared set admits that rule's type, and the rule that claims a posting is what
// resolves it -- there is no narrowing phase afterwards. Precedence is a number the
// rule carries rather than the order of a call site, so a per-broker ordering is a
// table rather than a restructuring. See
// docs/adr/0047-grouping-runs-as-precedence-ordered-passes.md.
package grouping

import (
	"context"
	"sort"

	"github.com/shopspring/decimal"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/txtype"
)

// Opts is what the rules need beyond the postings themselves. It carries the
// tolerances rather than reading them from anywhere, so Partition is a pure function
// and its tests state the numbers they depend on.
type Opts struct {
	// Money is the widest difference between two money figures that still reads as
	// the same amount. It is residual.Tolerance's money value said again, and
	// deliberately: a difference this rule would tolerate is one the balancer will
	// route to SOURCE_ROUNDING, so a pairing it accepts leaves a residual that
	// reads as the source disagreeing with itself rather than as a missing leg.
	Money decimal.Decimal
	// ConsiderationBand is the widest relative gap tolerated between a cash leg and
	// its trade's quantity * unit price.
	//
	// The two never agree exactly, because the export rounds the unit price it
	// quotes and the error that leaves is the half-digit it dropped as a fraction
	// of the price -- worst on a cheap unit, since the same half-digit is a larger
	// share of a smaller number. The widest correctly paired trade in the sample
	// exports is 0.56% out. This clears every real pairing while rejecting a
	// swapped cash row, which is off by whole percentage points.
	ConsiderationBand decimal.Decimal
}

// DefaultOpts is the calibrated configuration, measured against the sample exports
// by the converter rules this engine has to reproduce.
func DefaultOpts() Opts {
	return Opts{
		Money:             decimal.RequireFromString("0.005"),
		ConsiderationBand: decimal.RequireFromString("0.0075"),
	}
}

// DefaultRules is the ordering that applies to every source.
//
// A list rather than a fixed sequence of calls, because the ordering is expected to
// become per-broker: a single order has no one broker's data to justify it against,
// so an order right for one source may be wrong for the next. Precedence lives on
// the rules, so a variant list is a table and not a restructuring. See
// docs/adr/0047-grouping-runs-as-precedence-ordered-passes.md.
func DefaultRules() []Rule {
	return []Rule{Exact{}, Disposal(), Acquisition(), CashTrade(), Deposit(), Attaches{}}
}

// Member is one posting of a derived group and what the rule that claimed it
// resolved it to.
type Member struct {
	ID       string
	Resolved typev1.TxType
}

// Group is one economic event the engine derived: its postings, and what each of
// them resolved to. Members are ordered by id so two runs over the same postings
// produce identical output.
type Group struct {
	Members []Member
}

// Rule is one way of deciding that postings belong together.
type Rule interface {
	// Name identifies the rule in a log and in the stored record of which rule
	// placed a posting.
	Name() string
	// Precedence orders this rule against the others. Higher runs first, and the
	// order decides the partition rather than merely tuning it, because a claim is
	// irrevocable within a run.
	Precedence() int
	// Expand returns the postings this rule could link to any of ps, by making the
	// reads it needs.
	//
	// The rule issues its own reads rather than describing them for someone else to
	// dispatch, because it is the only thing that knows what it would compare. What
	// it may ask for is bounded by what the reader offers, which is how
	// docs/adr/0050-grouping-recomputes-a-neighbourhood.md's admissibility test --
	// a reach must be a bounded indexed query -- becomes structural rather than a
	// convention: a rule cannot state a reach that has no index behind it, because
	// there is no method for one.
	//
	// held is what the caller already has, passed through to the reader so a
	// posting is never read twice and each round shrinks.
	Expand(ctx context.Context, userID string, ps []db.GroupingPosting, r db.GroupingReader, held []string) ([]db.GroupingPosting, error)
	// Apply claims what this rule can from the postings still free.
	//
	// The rule drives its own claiming rather than handing proposals back for the
	// engine to walk, because two kinds of state bear on what it may take and only
	// one could be passed in ahead of time: what earlier rules took, and what this
	// rule itself has taken as it goes. A deposit run whose second opener must not
	// take the row its first opener just did is the second kind, and no list fixed
	// before the rule starts can express it without enumerating alternatives.
	//
	// Ranking is the rule's own. It ranks its candidates before taking any, which
	// is what stops one claim stranding another, and the key differs per rule --
	// reference proximity for a trade, ascending reference for a run -- so there is
	// nothing for the engine to do with it.
	//
	// State.Claim keeps the arbitration: it refuses a claim naming a posting that
	// has gone, so a rule cannot half-apply one however it is written.
	Apply(ps []db.GroupingPosting, st *State, opts Opts)
}

// Partition derives the groups of one neighbourhood.
//
// Pure and deterministic: the same postings in any order yield the same groups. A
// posting no rule claims is a group of its own, resolved to the common ancestor of
// its declared set, which is what keeps whatever the source knew rather than
// discarding it.
func Partition(ps []db.GroupingPosting, rules []Rule, opts Opts) []Group {
	ordered := make([]Rule, len(rules))
	copy(ordered, rules)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Precedence() != ordered[j].Precedence() {
			return ordered[i].Precedence() > ordered[j].Precedence()
		}
		return ordered[i].Name() < ordered[j].Name()
	})

	st := newState(ps)
	for _, r := range ordered {
		r.Apply(ps, st, opts)
	}
	return st.groups(ps)
}

// distinct drops the repeats from a rule's queries.
//
// Repeats are the common case rather than the exception: every posting of one account
// on one day asks the same date question, so a frontier of a hundred asks it once.
// This is what keeps a read proportional to the distinct questions rather than to the
// postings asking them.
func distinct[T comparable](in []T) []T {
	seen := make(map[T]bool, len(in))
	out := make([]T, 0, len(in))
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// State is the partition under construction: which postings share a group, and
// which have been given a role by a rule.
//
// A rule reads it to see what is left and writes to it by claiming. Everything else
// about it is the engine's, so a rule can neither observe the groups forming nor
// unpick one.
type State struct {
	parent   map[string]string
	resolved map[string]typev1.TxType
	claimed  map[string]bool
}

func newState(ps []db.GroupingPosting) *State {
	st := &State{
		parent:   make(map[string]string, len(ps)),
		resolved: make(map[string]typev1.TxType, len(ps)),
		claimed:  make(map[string]bool, len(ps)),
	}
	for _, p := range ps {
		st.parent[p.ID] = p.ID
	}
	return st
}

// find returns the group a posting currently belongs to, naming it by its lowest
// member id so the name does not depend on the order unions happened in.
func (s *State) find(id string) string {
	for s.parent[id] != id {
		s.parent[id] = s.parent[s.parent[id]]
		id = s.parent[id]
	}
	return id
}

func (s *State) union(a, b string) {
	ra, rb := s.find(a), s.find(b)
	if ra == rb {
		return
	}
	// Lowest id wins, so the representative is a property of the membership rather
	// than of the order the claims arrived in.
	if rb < ra {
		ra, rb = rb, ra
	}
	s.parent[rb] = ra
}

// Taken reports whether a posting already has a role, so a rule can pass over it
// while it looks for candidates.
func (s *State) Taken(id string) bool { return s.claimed[id] }

// Group names the group a posting currently sits in, so a rule can ask whether two
// postings are already together. The name is the group's lowest member id and means
// nothing outside this run.
//
// Read-only, and the only thing a rule may learn about the partition forming. It is
// what an attaching rule needs to tell "the token I point at is held by three legs of
// one event" from "it is held by two postings that are not together", which is the
// difference between an unambiguous attachment and no attachment at all.
func (s *State) Group(id string) string {
	if _, known := s.parent[id]; !known {
		return ""
	}
	return s.find(id)
}

// Claim records that these postings are legs of one event and resolves each to what
// the claiming rule concluded. It reports whether the claim was taken.
//
// All or nothing: a claim naming a posting that has gone is refused whole rather
// than applied to the rest. That is what makes a claim irrevocable -- the posting has
// a role, and a later claim wanting it is a later claim that loses
// (docs/adr/0047-grouping-runs-as-precedence-ordered-passes.md) -- and it is why a
// rule ranks its candidates before taking any rather than after.
//
// The refusal is whole rather than partial for a reason worth stating, because the
// partial reading looks like
// docs/adr/0048-correlations-declare-their-own-semantics.md's "a later pass may add
// to the group" and is not it. Keeping the free members and merging with the group
// holding the taken one lets a rule's own next claim attach to the claim it just
// made, so two purchases competing for one cash row end in a single group holding
// both rather than one keeping the row and the other going without -- which is
// precisely the stranding the ranking exists to prevent.
//
// So adding to a group an earlier rule assembled is not expressible here, and no
// rule needs it: each proposes a complete grouping rather than an extension of
// someone else's. A rule that wants to extend one will have to name which member is
// the anchor it attaches to and which are the postings it contributes, because that
// is the distinction this cannot infer.
func (s *State) Claim(ms ...Member) bool {
	if len(ms) < 2 {
		return false
	}
	for _, m := range ms {
		if _, known := s.parent[m.ID]; !known {
			return false
		}
		if s.claimed[m.ID] {
			return false
		}
	}
	for _, m := range ms {
		s.claimed[m.ID] = true
		s.resolved[m.ID] = m.Resolved
		s.union(ms[0].ID, m.ID)
	}
	return true
}

// Attach adds postings to the group an anchor already sits in, and resolves each to
// what the attaching rule concluded. It reports whether the attachment was taken.
//
// This is the distinction Claim's comment names as the one it cannot infer: which
// member is the anchor, and which are the postings being contributed. Claim cannot
// express it because it treats every member alike and refuses any claim naming a
// posting that has gone, which is right for a rule proposing a whole grouping and
// wrong for one adding a leg to somebody else's.
//
// It is not a hole in irrevocability. The anchor is read and never written: its
// group, its resolution and its claimed state all come out unchanged, so nothing an
// earlier rule decided is disturbed and no posting moves between groups. What is
// added is a posting that was free, which no rule had decided anything about. That
// is precisely
// docs/adr/0048-correlations-declare-their-own-semantics.md's "a later pass may add
// to the group but may not split it", and the only operation that can do it.
//
// A contributor that has already been claimed is refused rather than moved, and the
// refusal is whole, so a rule cannot half-apply an attachment. An anchor that is
// itself unclaimed is allowed: it then forms a group with the contributors, which is
// the same event under a weaker claim rather than a different one.
func (s *State) Attach(anchorID string, ms ...Member) bool {
	if len(ms) == 0 {
		return false
	}
	if _, known := s.parent[anchorID]; !known {
		return false
	}
	for _, m := range ms {
		if _, known := s.parent[m.ID]; !known {
			return false
		}
		if m.ID == anchorID || s.claimed[m.ID] {
			return false
		}
	}
	for _, m := range ms {
		s.claimed[m.ID] = true
		s.resolved[m.ID] = m.Resolved
		s.union(anchorID, m.ID)
	}
	return true
}

// groups reads the partition out, resolving whatever no rule claimed.
func (s *State) groups(ps []db.GroupingPosting) []Group {
	byRoot := map[string][]Member{}
	for _, p := range ps {
		r, ok := s.resolved[p.ID]
		if !ok {
			// Nothing narrowed it, so the common ancestor of the declared set is
			// what is left: the member itself for a singleton, the shared branch
			// for siblings, AMBIGUOUS across branches.
			r = txtype.Resolve(p.Declared)
		}
		root := s.find(p.ID)
		byRoot[root] = append(byRoot[root], Member{ID: p.ID, Resolved: r})
	}
	roots := make([]string, 0, len(byRoot))
	for root := range byRoot {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	out := make([]Group, 0, len(roots))
	for _, root := range roots {
		ms := byRoot[root]
		sort.Slice(ms, func(i, j int) bool { return ms[i].ID < ms[j].ID })
		out = append(out, Group{Members: ms})
	}
	return out
}
