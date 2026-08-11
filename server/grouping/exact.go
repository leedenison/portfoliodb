package grouping

import (
	"sort"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/txtype"
)

// ExactPrecedence is the highest of any rule, and deliberately so.
//
// A source that states its own grouping states it as a shared token -- OFX nests a
// trade's legs and the parser stamps the containing INVTRAN's FITID on each one --
// and a person asserting a grouping does the same thing with a token nobody
// transcribed. Re-deriving either by inference would replace a stated fact with a
// guess, so nothing may take a posting this rule wants.
const ExactPrecedence = 1000

// Exact groups postings that carry the same correlation token.
//
// An ordinary rule holding the highest precedence, and nothing more. The must-link
// behaviour this rule is usually described with -- a later rule may add to its group
// but may never take a member out of it -- belongs to the engine and holds for every
// rule's claims alike; docs/adr/0048-correlations-declare-their-own-semantics.md
// names it here only because a source stating two rows of a three-row event is where
// it visibly matters.
//
// What is particular to this rule is only what it compares and what it concludes:
// token equality, and nothing whatever about the kind of event.
type Exact struct{}

func (Exact) Name() string { return "exact" }

func (Exact) Precedence() int { return ExactPrecedence }

// Reach asks for everything correlated with each token this posting carries, in
// the same series. The index on (label, token) answers it directly, which is what
// makes this rule admissible: it starts from an identifier and asks who else holds
// it, rather than scanning for one.
func (Exact) Reach(p db.GroupingPosting) []Reach {
	var out []Reach
	for _, c := range p.Correlations {
		if !c.Declares(db.MatchExact) {
			continue
		}
		r := Reach{
			Kind:    ReachToken,
			Broker:  p.Broker,
			Account: p.Account,
			Label:   c.Label,
			Token:   c.Token,
		}
		// A token whose scope reaches past the account it was issued in has to be
		// looked for past it too, or the reach would be narrower than the rule.
		if c.Scope != db.ScopeAccount {
			r.AnyAccount = true
		}
		out = append(out, r)
	}
	return out
}

// Propose returns one claim per set of postings sharing a token, ranked by that
// token so the output does not depend on the order the postings arrived in.
//
// Ranking does no other work here. Two postings either carry the same token or they
// do not, so there is nothing to prefer between claims and nothing that can strand
// anything: the sets are disjoint by construction, because a token belongs to one
// series and a series to one set.
func (Exact) Propose(ps []db.GroupingPosting, _ Opts) []Claim {
	byToken := map[tokenKey][]db.GroupingPosting{}
	for _, p := range ps {
		for _, c := range p.Correlations {
			if !c.Declares(db.MatchExact) {
				continue
			}
			k := tokenKey{label: c.Label, token: c.Token, scope: c.Scope}
			switch c.Scope {
			case db.ScopeFile:
				// A file has no identity once its postings are rows, so the job
				// that supplied the correlation is what its scope names.
				k.within = p.JobID
			case db.ScopeAccount:
				k.within = brokerKey(p.Broker) + "\x00" + p.Account
			case db.ScopeBroker:
				k.within = brokerKey(p.Broker)
			}
			byToken[k] = append(byToken[k], p)
		}
	}

	keys := make([]tokenKey, 0, len(byToken))
	for k := range byToken {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].less(keys[j]) })

	var out []Claim
	for _, k := range keys {
		members := dedupe(byToken[k])
		if len(members) < 2 {
			continue
		}
		c := Claim{}
		for _, p := range members {
			// The rule states that these rows are one event; it states nothing
			// about what kind of event, because a shared identifier does not say.
			// So each member keeps whatever its own declaration narrows to, which
			// is the common ancestor of its candidate set.
			c.Members = append(c.Members, Member{ID: p.ID, Resolved: txtype.Resolve(p.Declared)})
		}
		out = append(out, c)
	}
	return out
}

// tokenKey is one comparability partition: a series, an identifier, and the set of
// postings the source says that identifier means anything across.
//
// The scope is part of the key rather than a filter applied to it. Two sources can
// legitimately issue the same string under the same label with different reach, and
// collapsing them would let a file-scoped reference match an account-scoped one
// silently.
type tokenKey struct {
	label  string
	token  string
	scope  string
	within string
}

func (k tokenKey) less(o tokenKey) bool {
	if k.label != o.label {
		return k.label < o.label
	}
	if k.token != o.token {
		return k.token < o.token
	}
	if k.scope != o.scope {
		return k.scope < o.scope
	}
	return k.within < o.within
}

// dedupe drops repeats and orders by id, since one posting can carry the same token
// twice and a claim names each member once.
func dedupe(ps []db.GroupingPosting) []db.GroupingPosting {
	seen := map[string]bool{}
	out := make([]db.GroupingPosting, 0, len(ps))
	for _, p := range ps {
		if seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// brokerKey names a broker for use in a comparability key.
func brokerKey(b typev1.Broker) string {
	return b.String()
}
