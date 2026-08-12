package postgres

import (
	"testing"
	"time"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// groupsOf reads back which group each posting sits in, and what it resolved to.
func groupsOf(t *testing.T, f *groupingFixture) map[string]string {
	t.Helper()
	rows, err := f.p.q.QueryContext(f.ctx,
		`SELECT id::text, group_id::text FROM txs WHERE user_id = $1::uuid AND account_type = 'USER'`,
		f.userID)
	if err != nil {
		t.Fatalf("read groups: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, group string
		if err := rows.Scan(&id, &group); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[id] = group
	}
	return out
}

func residualsOf(t *testing.T, f *groupingFixture) int {
	t.Helper()
	var n int
	err := f.p.q.QueryRowContext(f.ctx,
		`SELECT count(*) FROM txs WHERE user_id = $1::uuid AND synthetic_purpose IN (`+routedPurpose+`)`,
		f.userID).Scan(&n)
	if err != nil {
		t.Fatalf("count residuals: %v", err)
	}
	return n
}

// Two transfers stored as separate groups, each balanced by its own clearing leg --
// the shape a converter leaves when it cannot see two legs together. Joining them is
// the repair the engine exists to make.
func TestApplyGrouping_JoinsTwoStoredGroups(t *testing.T) {
	f := newGroupingFixture(t, "apply-join")
	ts := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)
	f.writeWithResidual(t, "A1", ts, "500", []typev1.TxType{typev1.TxType_TRANSFER},
		typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING)
	f.writeWithResidual(t, "A1", ts, "-500", []typev1.TxType{typev1.TxType_TRANSFER},
		typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING)

	before := groupsOf(t, f)
	if len(before) != 2 {
		t.Fatalf("stored %d transcribed postings, want 2", len(before))
	}
	var ids []string
	var changes []db.GroupMemberChange
	for id, group := range before {
		ids = append(ids, id)
		changes = append(changes, db.GroupMemberChange{
			ID: id, FromGroupID: group, Resolved: "TRANSFER", Moving: true,
		})
	}

	moved, err := f.p.ApplyGrouping(f.ctx, f.userID, []db.GroupChange{{Members: changes}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if moved != 2 {
		t.Fatalf("moved %d postings, want 2", moved)
	}

	after := groupsOf(t, f)
	if after[ids[0]] != after[ids[1]] {
		t.Fatalf("postings are in %s and %s, want one group", after[ids[0]], after[ids[1]])
	}
	if after[ids[0]] == before[ids[0]] {
		t.Fatal("the joined group kept a stored id, want a new one")
	}
	// The two legs cancel, so the joined group needs no residual at all -- which is
	// the point of joining them. Both old ones are gone.
	if n := residualsOf(t, f); n != 0 {
		t.Fatalf("%d residuals left, want none: the joined group balances", n)
	}
}

// The regroup and the re-routing commit together. Every state in between is
// unbalanced -- a posting moved out leaves its group short until the residual is
// routed again -- and the deferred constraint is what makes that expressible. If the
// transaction were split, this would fail at the first COMMIT.
func TestApplyGrouping_LeavesEveryGroupBalanced(t *testing.T) {
	f := newGroupingFixture(t, "apply-balance")
	ts := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)
	f.writeWithResidual(t, "A1", ts, "500", []typev1.TxType{typev1.TxType_TRANSFER},
		typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING)
	f.writeWithResidual(t, "A1", ts, "-300", []typev1.TxType{typev1.TxType_TRANSFER},
		typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING)

	before := groupsOf(t, f)
	var changes []db.GroupMemberChange
	for id, group := range before {
		changes = append(changes, db.GroupMemberChange{
			ID: id, FromGroupID: group, Resolved: "TRANSFER", Moving: true,
		})
	}
	if _, err := f.p.ApplyGrouping(f.ctx, f.userID, []db.GroupChange{{Members: changes}}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// 500 and -300 leave 200 over, so the joined group carries exactly one routed
	// counterparty rather than the two it started with.
	if n := residualsOf(t, f); n != 1 {
		t.Fatalf("%d residuals, want 1 for what the joined legs leave over", n)
	}
	// Draining forces the deferred triggers to fire now. testDBTx rolls back and
	// never commits, so without this the constraint would never be evaluated and
	// the test would prove nothing about balance at all.
	if err := drainBalanceChecks(t, f.p); err != nil {
		t.Fatalf("balance check after regroup: %v", err)
	}
}

// A retype settles a posting where it stands, because the resolved value is derived
// from the partition and can change while the membership does not.
func TestApplyGrouping_RetypesWithoutMoving(t *testing.T) {
	f := newGroupingFixture(t, "apply-retype")
	ts := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)
	f.writeWithResidual(t, "A1", ts, "500", []typev1.TxType{typev1.TxType_TRADE_CASH, typev1.TxType_TRANSFER},
		typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING)

	before := groupsOf(t, f)
	var id string
	for k := range before {
		id = k
	}
	if _, err := f.p.ApplyGrouping(f.ctx, f.userID, []db.GroupChange{{
		Members: []db.GroupMemberChange{{ID: id, Resolved: "TRANSFER"}},
	}}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	after := groupsOf(t, f)
	if after[id] != before[id] {
		t.Fatalf("posting moved to %s, want it left in %s", after[id], before[id])
	}
	var resolved string
	if err := f.p.q.QueryRowContext(f.ctx, `SELECT resolved_tx_type FROM txs WHERE id = $1::uuid`, id).Scan(&resolved); err != nil {
		t.Fatalf("read resolved: %v", err)
	}
	if resolved != "TRANSFER" {
		t.Fatalf("resolved = %q, want TRANSFER", resolved)
	}
}

// Nothing to change is not an empty transaction, it is no transaction: a cycle that
// agrees everywhere touches nothing at all.
func TestApplyGrouping_NoChangesWritesNothing(t *testing.T) {
	f := newGroupingFixture(t, "apply-none")
	ts := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)
	f.writeWithResidual(t, "A1", ts, "500", []typev1.TxType{typev1.TxType_TRANSFER},
		typev1.AccountType_ACCOUNT_TYPE_TRANSFER_CLEARING)

	before := groupsOf(t, f)
	moved, err := f.p.ApplyGrouping(f.ctx, f.userID, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if moved != 0 {
		t.Fatalf("moved %d postings, want 0", moved)
	}
	after := groupsOf(t, f)
	for id, group := range before {
		if after[id] != group {
			t.Fatalf("posting %s moved from %s to %s", id, group, after[id])
		}
	}
	if n := residualsOf(t, f); n != 1 {
		t.Fatalf("%d residuals, want the original 1 untouched", n)
	}
}
