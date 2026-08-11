package grouping

import (
	"sort"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// Diff is what the engine would change about the partition already stored.
//
// A derived group whose membership is exactly a stored group's produces nothing,
// however the two were arrived at. That is the whole of what keeps a cycle from
// churning ids: the engine runs over a neighbourhood far wider than any upload, and
// most of what it sees it agrees with.
//
// Where it does agree about membership it may still disagree about type, because the
// resolved value is derived from the partition and a rule settles it when it claims.
// Those are carried as members that are not moving, so the group keeps its id and its
// postings are retyped where they stand.
func Diff(ps []db.GroupingPosting, gs []Group) []db.GroupChange {
	stored := map[string][]string{}
	storedOf := map[string]string{}
	resolvedOfStored := map[string]string{}
	for _, p := range ps {
		stored[p.GroupID] = append(stored[p.GroupID], p.ID)
		storedOf[p.ID] = p.GroupID
		resolvedOfStored[p.ID] = p.Resolved
	}
	for _, ids := range stored {
		sort.Strings(ids)
	}

	var out []db.GroupChange
	for _, g := range gs {
		ids := make([]string, 0, len(g.Members))
		for _, m := range g.Members {
			ids = append(ids, m.ID)
		}
		sort.Strings(ids)

		if same(stored[storedOf[ids[0]]], ids) {
			// Membership agrees. Anything left is a retype, and a group with none
			// of those is not a change at all.
			var c db.GroupChange
			for _, m := range g.Members {
				if resolvedOfStored[m.ID] == resolvedStr(m.Resolved) {
					continue
				}
				c.Members = append(c.Members, db.GroupMemberChange{
					ID:       m.ID,
					Resolved: resolvedStr(m.Resolved),
				})
			}
			if len(c.Members) > 0 {
				out = append(out, c)
			}
			continue
		}

		c := db.GroupChange{}
		for _, m := range g.Members {
			c.Members = append(c.Members, db.GroupMemberChange{
				ID:          m.ID,
				FromGroupID: storedOf[m.ID],
				Resolved:    resolvedStr(m.Resolved),
				Moving:      true,
			})
		}
		sort.Slice(c.Members, func(i, j int) bool { return c.Members[i].ID < c.Members[j].ID })
		out = append(out, c)
	}
	return out
}

// same reports whether two sorted id lists hold the same postings.
func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// resolvedStr is the stored spelling of a resolved type, empty where the vocabulary
// does not know it. Empty never equals a stored value, so an unknown type reads as a
// change and is written -- which fails loudly at the column CHECK rather than quietly
// leaving a posting typed as something it is not.
func resolvedStr(t typev1.TxType) string {
	v, err := db.TxTypeToStr(t)
	if err != nil {
		return ""
	}
	return v
}
