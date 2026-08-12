package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/leedenison/portfoliodb/server/db"
)

// newGroupSQL creates the group a regrouped posting moves into. It takes the
// timestamp of the earliest posting joining it, as an ingested group takes the
// timestamp of its first leg, and no job: a group the engine drew was not written by
// an upload.
const newGroupSQL = `
	INSERT INTO tx_groups (user_id, timestamp)
	SELECT $1::uuid, MIN(t.timestamp)
	FROM txs t WHERE t.id = ANY($2::uuid[])
	RETURNING id
`

// moveGroupSQL reassigns a posting and settles what the claiming rule resolved it
// to. The two travel together because the rule that claims a row is what resolves it,
// so a partition written without the resolution would leave a group whose residual is
// classified against types nothing derived.
const moveGroupSQL = `
	UPDATE txs SET group_id = $1::uuid, resolved_tx_type = $2
	WHERE id = ANY($3::uuid[])
`

// resolveOnlySQL settles a posting the engine typed differently without moving it,
// for a group whose membership it agreed with.
const resolveOnlySQL = `
	UPDATE txs SET resolved_tx_type = $1 WHERE id = ANY($2::uuid[])
`

// dropResidualsSQL removes the routed counterparties of the groups a regroup
// touched, so they can be routed fresh against the membership that now holds.
//
// A residual carries no evidence of its own, so it cannot be repartitioned; it is
// arithmetic on the legs of the group it sits in, and once those move it is
// arithmetic on nothing.
const dropResidualsSQL = `
	DELETE FROM txs
	WHERE group_id = ANY($1::uuid[])
	  AND synthetic_purpose IN (` + routedPurpose + `)
`

// ApplyGrouping implements db.GroupingDB.
//
// One transaction, because check_tx_group_balance() is DEFERRABLE INITIALLY DEFERRED
// and its update trigger fires on group_id changing. Every intermediate state here is
// unbalanced -- a posting moved out of a group leaves it short until the residual is
// routed again -- and the deferral is what makes that expressible at all. Leaving the
// re-routing to a later statement would expose a moment where the constraint fires on
// data that was valid before the regroup began.
func (p *Postgres) ApplyGrouping(ctx context.Context, userID string, changes []db.GroupChange) (int, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return 0, fmt.Errorf("invalid user id: %w", err)
	}
	if len(changes) == 0 {
		return 0, nil
	}
	moved := 0
	err = p.runInTx(ctx, func(exec queryable) error {
		var touched settleSet
		moved, err = applyChanges(ctx, exec, userUUID, changes, &touched)
		if err != nil {
			return err
		}
		return touched.settle(ctx, exec, userUUID)
	})
	return moved, err
}

// applyChanges moves the postings the engine repartitioned and clears what the groups
// it touched owed, recording them in touched for the caller to settle.
//
// It writes only what the engine disagrees with. A group whose membership the engine
// drew exactly as stored produces no statement, keeps its id, and keeps the transfer
// matches keyed on that id. That is what stops a cycle over a neighbourhood far wider
// than an upload from churning ids for postings nobody touched. See
// docs/adr/0047-grouping-runs-as-precedence-ordered-passes.md.
//
// The caller settles rather than this, because a caller that also inserted or deleted
// postings has groups of its own to settle and they have to be balanced together.
func applyChanges(ctx context.Context, exec queryable, userUUID uuid.UUID, changes []db.GroupChange, touched *settleSet) (int, error) {
	moved := 0
	// A group a posting left is short by its value and could be short by any
	// amount; one the engine assembled, or agreed with and only retyped, holds every
	// leg its source stated. That is the difference between the two residual rules.
	for _, c := range changes {
		for _, m := range c.Members {
			if m.FromGroupID != "" {
				touched.add(m.FromGroupID, m.Moving)
			}
		}
	}

	for _, c := range changes {
		var move []string
		movingByType := map[string][]string{}
		stayingByType := map[string][]string{}
		for _, m := range c.Members {
			if m.Moving {
				move = append(move, m.ID)
				movingByType[m.Resolved] = append(movingByType[m.Resolved], m.ID)
				continue
			}
			stayingByType[m.Resolved] = append(stayingByType[m.Resolved], m.ID)
		}
		// A posting the engine agreed about but retyped is settled where it
		// stands. The type is derived from the partition, so it can change
		// while the membership does not.
		for resolved, ids := range stayingByType {
			if _, err := exec.ExecContext(ctx, resolveOnlySQL, resolved, pq.Array(ids)); err != nil {
				return moved, fmt.Errorf("resolve postings: %w", err)
			}
		}
		if len(move) == 0 {
			continue
		}
		var groupID uuid.UUID
		if err := exec.QueryRowContext(ctx, newGroupSQL, userUUID, pq.Array(move)).Scan(&groupID); err != nil {
			return moved, fmt.Errorf("create group: %w", err)
		}
		// One statement per resolved type rather than one for the group. The rule
		// that claims a posting resolves that posting, and the legs of one event do
		// not resolve alike -- a trade's asset leg is TRADE_ASSET and the cash it
		// settles through is TRADE_CASH. Stamping the group with a single value
		// would retype one of them to the other's.
		for resolved, ids := range movingByType {
			if _, err := exec.ExecContext(ctx, moveGroupSQL, groupID, resolved, pq.Array(ids)); err != nil {
				return moved, fmt.Errorf("move postings: %w", err)
			}
		}
		touched.add(groupID.String(), false)
		moved += len(move)
	}

	ids := pq.Array(touched.ids())
	if _, err := exec.ExecContext(ctx, dropResidualsSQL, ids); err != nil {
		return moved, fmt.Errorf("drop residuals: %w", err)
	}
	// A group the regroup emptied goes with its residuals, and one that kept
	// something is re-dated to what it has left, exactly as a period replace
	// leaves them.
	if _, err := exec.ExecContext(ctx, deleteEmptiedGroupsSQL, ids); err != nil {
		return moved, fmt.Errorf("delete emptied groups: %w", err)
	}
	if _, err := exec.ExecContext(ctx, repointGroupTimestampsSQL, ids); err != nil {
		return moved, fmt.Errorf("repoint group timestamps: %w", err)
	}
	// The matches naming a group whose membership moved are cache, and the
	// matcher rebuilds them from the same evidence on its next cycle.
	if _, err := exec.ExecContext(ctx, deleteTouchedMatchesSQL, ids); err != nil {
		return moved, fmt.Errorf("delete touched matches: %w", err)
	}
	return moved, nil
}
