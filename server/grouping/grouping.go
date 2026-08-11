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

// Claim is one rule's proposal that these postings are legs of one event.
//
// A claim may name a posting an earlier rule already took. The effect is to merge
// that posting's group with the rest of the claim, and only the members the claim
// newly takes are resolved by it; an already-claimed member keeps the type the rule
// that took it gave.
//
// So every claim is a must-link: a later rule may add to a group but can never take
// a member out of one. That is
// docs/adr/0047-grouping-runs-as-precedence-ordered-passes.md's irrevocable claim
// seen from the other side, and it holds for every rule rather than for any
// particular kind of evidence.
// docs/adr/0048-correlations-declare-their-own-semantics.md names the case that
// makes it matter -- a source stating two rows of a three-row event, where refusing
// the merge would exclude the third.
type Claim struct {
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
	// Propose returns the groupings this rule makes over ps, ranked best first.
	//
	// It sees every posting of the neighbourhood, claimed or not, because a claim
	// may merge with a group an earlier rule assembled. It does not need to know
	// what has been claimed: the engine discards a proposal that would re-role a
	// posting or join two existing groups, so a rule is a pure function of the
	// postings it is handed.
	//
	// Ranking is the rule's own and is what stops one claim stranding another --
	// the engine takes proposals in the order given and skips any that conflict
	// with one already accepted, so a rule that returns its best pairings first
	// gets them.
	Propose(ps []db.GroupingPosting, opts Opts) []Claim
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
		for _, c := range r.Propose(ps, opts) {
			st.accept(c)
		}
	}
	return st.groups(ps)
}

// state is the partition under construction: which postings share a group, and
// which have been given a role by a rule.
type state struct {
	parent   map[string]string
	resolved map[string]typev1.TxType
	claimed  map[string]bool
}

func newState(ps []db.GroupingPosting) *state {
	st := &state{
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
func (s *state) find(id string) string {
	for s.parent[id] != id {
		s.parent[id] = s.parent[s.parent[id]]
		id = s.parent[id]
	}
	return id
}

func (s *state) union(a, b string) {
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

// accept takes a claim if it is still coherent with what has been decided.
//
// Two things disqualify one. A claim naming no unclaimed member would only rearrange
// what earlier rules concluded, and a claim touching two existing groups would join
// what a higher-precedence rule kept apart -- which is a different thing from adding
// to a group, and is the half of "may add but may not split" that needs enforcing.
func (s *state) accept(c Claim) bool {
	if len(c.Members) < 2 {
		return false
	}
	anchors := map[string]bool{}
	fresh := 0
	for _, m := range c.Members {
		if _, known := s.parent[m.ID]; !known {
			return false
		}
		if s.claimed[m.ID] {
			anchors[s.find(m.ID)] = true
			continue
		}
		fresh++
	}
	if fresh == 0 || len(anchors) > 1 {
		return false
	}
	for _, m := range c.Members {
		if !s.claimed[m.ID] {
			s.claimed[m.ID] = true
			s.resolved[m.ID] = m.Resolved
		}
		s.union(c.Members[0].ID, m.ID)
	}
	return true
}

// groups reads the partition out, resolving whatever no rule claimed.
func (s *state) groups(ps []db.GroupingPosting) []Group {
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
