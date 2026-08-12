package grouping

import (
	"context"

	"github.com/leedenison/portfoliodb/server/db"
)

// Engine is the rules and tolerances a partition is derived with, as a db.Settler.
//
// The same three steps the cadence worker runs -- grow the region, partition it,
// keep only the disagreements -- offered to the store so that an upload is
// partitioned in the transaction that writes it. What differs is only where the
// seed comes from: postings that just arrived here, groups carrying an unresolved
// residual there.
type Engine struct {
	Rules []Rule
	Opts  Opts
}

// NewEngine returns the ordering and tolerances that apply to every source.
func NewEngine() Engine {
	return Engine{Rules: DefaultRules(), Opts: DefaultOpts()}
}

// Settle implements db.Settler.
//
// One neighbourhood per seed, and the ones that overlap are answered from the same
// reads because a posting already held is never asked for again. A seed swallowed by
// an earlier neighbourhood costs one round that finds nothing.
func (e Engine) Settle(ctx context.Context, userID string, seed []db.GroupingPosting, r db.GroupingReader) ([]db.GroupChange, error) {
	rules := e.Rules
	if len(rules) == 0 {
		rules = DefaultRules()
	}
	done := map[string]bool{}
	var out []db.GroupChange
	for _, s := range seed {
		if done[s.ID] {
			continue
		}
		ps, err := Neighbourhood(ctx, userID, []db.GroupingPosting{s}, rules, r)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			done[p.ID] = true
		}
		out = append(out, Diff(ps, Partition(ps, rules, e.Opts))...)
	}
	return out, nil
}
