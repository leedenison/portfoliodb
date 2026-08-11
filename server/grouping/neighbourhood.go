package grouping

import (
	"context"
	"fmt"
	"sort"

	"github.com/leedenison/portfoliodb/server/db"
)

// Neighbourhood grows a seed until no rule can reach anything new.
//
// The region a partition is computed over is not a window. A user-asserted
// correlation links a posting to one years away and no window contains that, so the
// region is the fixpoint of the rules' own candidate relations: ask every rule what
// it could link each posting to, add what comes back, and repeat.
//
// It terminates because it only ever adds. Each round contributes postings the
// caller did not hold, the held set is bounded by what the user has, so the fixpoint
// arrives in at most as many rounds as there are postings. That is the whole
// termination argument, and it is why the engine recomputes a region rather than
// repairing one -- see docs/adr/0050-grouping-recomputes-a-neighbourhood.md.
//
// The worst case is that the region reaches everything the user has, which is a full
// partition: correct, and merely slow. Nothing here caps it, because a cap would trade
// a slow answer for a wrong one.
func Neighbourhood(ctx context.Context, userID string, seed []db.GroupingPosting, rules []Rule, r db.GroupingReader) ([]db.GroupingPosting, error) {
	held := make(map[string]db.GroupingPosting, len(seed))
	for _, p := range seed {
		held[p.ID] = p
	}
	frontier := seed
	for len(frontier) > 0 {
		var fresh []db.GroupingPosting
		for _, rule := range rules {
			found, err := rule.Expand(ctx, userID, frontier, r, ids(held))
			if err != nil {
				return nil, fmt.Errorf("%s: expand neighbourhood: %w", rule.Name(), err)
			}
			// Filtered here as well as asked for in the read. A round counts only
			// what the caller did not have, so the loop shrinks to nothing whatever
			// a store returns, and termination is this function's property rather
			// than a promise made elsewhere.
			for _, p := range found {
				if _, dup := held[p.ID]; dup {
					continue
				}
				held[p.ID] = p
				fresh = append(fresh, p)
			}
		}
		frontier = fresh
	}

	out := make([]db.GroupingPosting, 0, len(held))
	for _, p := range held {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ids returns the held postings' ids, sorted, so a fetch is given a stable exclusion
// list and two runs over the same data issue the same queries.
func ids(held map[string]db.GroupingPosting) []string {
	out := make([]string, 0, len(held))
	for id := range held {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
