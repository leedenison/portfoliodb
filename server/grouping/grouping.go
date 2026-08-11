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
	"sort"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/txtype"
)

// Opts is what the rules need beyond the postings themselves. It carries the
// tolerances rather than reading them from anywhere, so Partition is a pure function
// and its tests state the numbers they depend on.
type Opts struct {
	// Money is the widest difference between two money figures that still reads as
	// the same amount. It is residual.Tolerance's money value, and it is here
	// rather than imported so a rule's band and the balancer's cannot drift apart
	// silently: they are the same claim about how finely a source quotes money.
	Money string
	// ConsiderationBand is the widest relative gap tolerated between a cash leg and
	// its trade's quantity * unit price. The export rounds the price it quotes, and
	// the error that leaves is worst on a cheap unit.
	ConsiderationBand string
}

// DefaultOpts is the calibrated configuration, measured against the sample exports
// by the converter rules this engine has to reproduce.
func DefaultOpts() Opts {
	return Opts{Money: "0.005", ConsiderationBand: "0.0075"}
}

// DefaultRules is the ordering that applies to every source.
//
// A list rather than a fixed sequence of calls, because the ordering is expected to
// become per-broker: a single order has no one broker's data to justify it against,
// so an order right for one source may be wrong for the next. Precedence lives on
// the rules, so a variant list is a table and not a restructuring. See
// docs/adr/0047-grouping-runs-as-precedence-ordered-passes.md.
func DefaultRules() []Rule {
	return []Rule{Exact{}}
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
	// Reach states what this rule could link p to, as queries a store can answer
	// from an index. It is what seeds the neighbourhood closure, and a rule that
	// cannot state one is not admissible -- nothing could compute the region it
	// needs. See docs/adr/0050-grouping-recomputes-a-neighbourhood.md.
	Reach(p db.GroupingPosting) []Reach
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
