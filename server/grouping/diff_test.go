package grouping

import (
	"testing"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// resolved records what a previous cycle concluded about a posting, which is what a
// retype is measured against.
func resolvedAs(p db.GroupingPosting, t typev1.TxType) db.GroupingPosting {
	p.Resolved = resolvedStr(t)
	return p
}

// The property the whole design rests on: a group the engine drew exactly as stored
// produces no statement at all, so it keeps its id and the transfer matches keyed on
// it. Without this a cycle over a neighbourhood far wider than an upload would churn
// ids for postings nobody touched.
func TestDiff_WritesNothingWhereItAgrees(t *testing.T) {
	ps := []db.GroupingPosting{
		resolvedAs(stored(posting("a", typev1.TxType_TRADE_ASSET), "g1"), typev1.TxType_TRADE_ASSET),
		resolvedAs(stored(posting("b", typev1.TxType_TRADE_CASH), "g1"), typev1.TxType_TRADE_CASH),
		resolvedAs(stored(posting("c", typev1.TxType_HOLDING_COST), "g2"), typev1.TxType_HOLDING_COST),
	}
	gs := []Group{
		{Members: []Member{
			{ID: "a", Resolved: typev1.TxType_TRADE_ASSET},
			{ID: "b", Resolved: typev1.TxType_TRADE_CASH},
		}},
		{Members: []Member{{ID: "c", Resolved: typev1.TxType_HOLDING_COST}}},
	}
	if got := Diff(ps, gs); len(got) != 0 {
		t.Fatalf("diff = %+v, want nothing", got)
	}
}

// A group the engine drew differently moves whole, and every member carries the group
// it is leaving so that group's residuals can be routed again.
func TestDiff_MovesAGroupItDrewDifferently(t *testing.T) {
	ps := []db.GroupingPosting{
		resolvedAs(stored(posting("a", typev1.TxType_TRADE_ASSET), "g1"), typev1.TxType_AMBIGUOUS),
		resolvedAs(stored(posting("b", typev1.TxType_TRADE_CASH), "g2"), typev1.TxType_AMBIGUOUS),
	}
	gs := []Group{{Members: []Member{
		{ID: "a", Resolved: typev1.TxType_TRADE_ASSET},
		{ID: "b", Resolved: typev1.TxType_TRADE_CASH},
	}}}

	got := Diff(ps, gs)
	if len(got) != 1 || len(got[0].Members) != 2 {
		t.Fatalf("diff = %+v, want one change of two members", got)
	}
	for _, m := range got[0].Members {
		if !m.Moving {
			t.Fatalf("member %s is not moving, want the whole group to move", m.ID)
		}
	}
	if got[0].Members[0].FromGroupID != "g1" || got[0].Members[1].FromGroupID != "g2" {
		t.Fatalf("members leave %q and %q, want g1 and g2",
			got[0].Members[0].FromGroupID, got[0].Members[1].FromGroupID)
	}
}

// The resolved value is derived from the partition, so it can change while the
// membership does not -- a Cash In that stays where it is but is settled as a
// trade's cash leg rather than staying ambiguous. That is a retype in place, and the
// group keeps its id.
func TestDiff_RetypesWithoutMoving(t *testing.T) {
	ps := []db.GroupingPosting{
		resolvedAs(stored(posting("a", typev1.TxType_TRADE_ASSET), "g1"), typev1.TxType_TRADE_ASSET),
		resolvedAs(stored(posting("b", typev1.TxType_TRADE_CASH, typev1.TxType_TRANSFER), "g1"), typev1.TxType_AMBIGUOUS),
	}
	gs := []Group{{Members: []Member{
		{ID: "a", Resolved: typev1.TxType_TRADE_ASSET},
		{ID: "b", Resolved: typev1.TxType_TRADE_CASH},
	}}}

	got := Diff(ps, gs)
	if len(got) != 1 {
		t.Fatalf("diff = %+v, want one change", got)
	}
	if len(got[0].Members) != 1 || got[0].Members[0].ID != "b" {
		t.Fatalf("change names %+v, want only the retyped posting", got[0].Members)
	}
	if got[0].Members[0].Moving {
		t.Fatal("retyped posting is moving, want it settled where it stands")
	}
	if got[0].Members[0].Resolved != "TRADE_CASH" {
		t.Fatalf("resolved = %q, want TRADE_CASH", got[0].Members[0].Resolved)
	}
}

// A group the engine split writes both halves, since neither membership is the one
// stored.
func TestDiff_SplitsWriteBothHalves(t *testing.T) {
	ps := []db.GroupingPosting{
		resolvedAs(stored(posting("a", typev1.TxType_TRADE_ASSET), "g1"), typev1.TxType_TRADE_ASSET),
		resolvedAs(stored(posting("b", typev1.TxType_TRADE_CASH), "g1"), typev1.TxType_TRADE_CASH),
		resolvedAs(stored(posting("c", typev1.TxType_HOLDING_COST), "g1"), typev1.TxType_HOLDING_COST),
	}
	gs := []Group{
		{Members: []Member{
			{ID: "a", Resolved: typev1.TxType_TRADE_ASSET},
			{ID: "b", Resolved: typev1.TxType_TRADE_CASH},
		}},
		{Members: []Member{{ID: "c", Resolved: typev1.TxType_HOLDING_COST}}},
	}
	got := Diff(ps, gs)
	if len(got) != 2 {
		t.Fatalf("diff = %+v, want both halves written", got)
	}
}
