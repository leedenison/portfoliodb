package grouping

import (
	"context"
	"sort"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/txtype"
)

// AttachesPrecedence is below every rule that decides a partition, and has to be.
//
// A pointer says nothing about the posting it names, so it cannot decide where that
// posting goes -- it can only follow. Running it above the trade rules would let it
// claim a trade's asset leg before those rules could pair it with its cash leg,
// stranding the cash row: the group would be the charge and the trade rather than the
// trade and the money that settled it.
const AttachesPrecedence = 500

// Attaches adds a posting to the group of the posting its source named.
//
// One rule for every source that reports a subordinate leg as a record of its own
// while saying which record it belongs under: a charge a converter could tie to its
// trade, a withheld tax naming its dividend, cash in lieu naming its corporate
// action. What differs between those is which posting the pointer names and why the
// converter believed it, and neither is this rule's business -- it reads a token,
// finds who carries it, and attaches. Broker-specific knowledge stays in the
// converter that has it, which is the point of expressing that knowledge as evidence
// rather than as a partition (docs/adr/0052-an-attaching-correlation-is-additive.md).
type Attaches struct{}

func (Attaches) Name() string { return "attaches" }

func (Attaches) Precedence() int { return AttachesPrecedence }

// Expand asks who carries each token these postings point at.
//
// The same indexed question Exact asks, and deliberately the same method: a pointer
// names an identifier, and finding who holds an identifier is what
// PostingsByToken does. So this rule states a reach that already has an index behind
// it (docs/adr/0050-grouping-recomputes-a-neighbourhood.md) without adding one.
//
// Asked in one direction only. A posting that carries a token does not thereby reach
// the postings pointing at it, because it says nothing about them and a bare
// identifier would otherwise drag in every reference near it. The bearer reaches its
// target; the target is found because some bearer named it.
func (Attaches) Expand(ctx context.Context, userID string, ps []db.GroupingPosting, r db.GroupingReader, held []string) ([]db.GroupingPosting, error) {
	var qs []db.TokenQuery
	for _, p := range ps {
		for _, c := range p.Correlations {
			if !c.Declares(db.MatchAttaches) {
				continue
			}
			qs = append(qs, db.TokenQuery{
				Broker:  p.Broker,
				Account: p.Account,
				// A token whose scope reaches past the account it was issued in
				// has to be looked for past it too.
				AnyAccount: c.Scope != db.ScopeAccount,
				Label:      c.Label,
				Token:      c.Token,
			})
		}
	}
	if len(qs) == 0 {
		return nil, nil
	}
	return r.PostingsByToken(ctx, userID, distinct(qs), held)
}

// Apply attaches each free pointer to the group holding the token it names.
//
// No ranking, because there is nothing to prefer between attachments and nothing one
// can take from another: a pointer names one identifier, every posting it could
// attach to is found by that name alone, and two pointers naming the same target both
// attach to it. What ranking exists to prevent -- one claim stranding another -- needs
// two rules competing for one posting, and a contributor is wanted by exactly one
// pointer: its own.
//
// Bearers are walked in id order so the output does not depend on the order the
// postings arrived in.
func (Attaches) Apply(ps []db.GroupingPosting, st *State, _ Opts) {
	// Who carries what, over the postings that state an identifier of their own.
	// Keyed by comparability partition as Exact keys it, so a file-scoped token is
	// never matched against an account-scoped one that happens to read the same.
	carriers := map[tokenKey][]string{}
	for _, p := range ps {
		for _, c := range p.Correlations {
			if !c.Declares(db.MatchExact) {
				continue
			}
			carriers[keyFor(p, c.Label, c.Token, c.Scope)] = append(carriers[keyFor(p, c.Label, c.Token, c.Scope)], p.ID)
		}
	}

	bearers := make([]db.GroupingPosting, 0, len(ps))
	for _, p := range ps {
		if st.Taken(p.ID) {
			continue
		}
		bearers = append(bearers, p)
	}
	sort.Slice(bearers, func(i, j int) bool { return bearers[i].ID < bearers[j].ID })

	for _, p := range bearers {
		for _, c := range p.Correlations {
			if !c.Declares(db.MatchAttaches) {
				continue
			}
			anchor, ok := anchorOf(carriers[keyFor(p, c.Label, c.Token, c.Scope)], p.ID, st)
			if !ok {
				continue
			}
			// The rule states that this posting is a leg of that event, and
			// nothing about what kind of leg: the pointer says where it
			// belongs, not what it is. So it keeps whatever its own
			// declaration narrows to, as an unclaimed posting would.
			if st.Attach(anchor, Member{ID: p.ID, Resolved: txtype.Resolve(p.Declared)}) {
				break
			}
		}
	}
}

// anchorOf returns the posting an attachment should join, and whether the pointer
// identified one.
//
// Several postings can carry one token -- an OFX record stamps its FITID on every leg
// it produced -- and that is not an ambiguity while they are together, because the
// pointer names the event and any member of it names the same group. It is an
// ambiguity when they are not: the pointer would then be choosing between two events,
// which it has no evidence to do, and a wrong attachment is worse than none.
//
// The bearer's own id is excluded. A posting carrying a token and pointing at the
// same one names itself, which is not a statement about where it belongs.
func anchorOf(carrierIDs []string, bearerID string, st *State) (string, bool) {
	anchor, group := "", ""
	for _, id := range carrierIDs {
		if id == bearerID {
			continue
		}
		g := st.Group(id)
		switch {
		case anchor == "":
			anchor, group = id, g
		case g != group:
			return "", false
		case id < anchor:
			// Lowest id among the members of one group, so which member is
			// named does not depend on the order they were read in.
			anchor = id
		}
	}
	return anchor, anchor != ""
}

// keyFor names the comparability partition a token sits in for this posting: the
// series, the identifier, and the set of postings the source says it means anything
// across. Shared with Exact so the two cannot disagree about what is comparable.
func keyFor(p db.GroupingPosting, label, token, scope string) tokenKey {
	k := tokenKey{label: label, token: token, scope: scope}
	switch scope {
	case db.ScopeFile:
		k.within = p.JobID
	case db.ScopeAccount:
		k.within = brokerKey(p.Broker) + "\x00" + p.Account
	case db.ScopeBroker:
		k.within = brokerKey(p.Broker)
	}
	return k
}
