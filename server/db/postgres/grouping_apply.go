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
	  AND account_type IN (` + residualAccountTypes + `)
`

// ApplyGrouping implements db.GroupingDB.
//
// One transaction, because check_tx_group_balance() is DEFERRABLE INITIALLY DEFERRED
// and its update trigger fires on group_id changing. Every intermediate state here is
// unbalanced -- a posting moved out of a group leaves it short until the residual is
// routed again -- and the deferral is what makes that expressible at all. Leaving the
// re-routing to a later statement would expose a moment where the constraint fires on
// data that was valid before the regroup began.
//
// It writes only what it disagrees with. A group whose membership the engine drew
// exactly as stored produces no statement, keeps its id, and keeps the transfer
// matches keyed on that id. That is what stops a cycle over a neighbourhood far wider
// than an upload from churning ids for postings nobody touched. See
// docs/adr/0047-grouping-runs-as-precedence-ordered-passes.md.
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
		touched := map[string]bool{}
		for _, c := range changes {
			for _, m := range c.Members {
				if m.FromGroupID != "" {
					touched[m.FromGroupID] = true
				}
			}
		}

		for _, c := range changes {
			var move []string
			var moveType string
			byType := map[string][]string{}
			for _, m := range c.Members {
				if m.Moving {
					move = append(move, m.ID)
					moveType = m.Resolved
					continue
				}
				byType[m.Resolved] = append(byType[m.Resolved], m.ID)
			}
			// A posting the engine agreed about but retyped is settled where it
			// stands. The type is derived from the partition, so it can change
			// while the membership does not.
			for resolved, ids := range byType {
				if _, err := exec.ExecContext(ctx, resolveOnlySQL, resolved, pq.Array(ids)); err != nil {
					return fmt.Errorf("resolve postings: %w", err)
				}
			}
			if len(move) == 0 {
				continue
			}
			var groupID uuid.UUID
			if err := exec.QueryRowContext(ctx, newGroupSQL, userUUID, pq.Array(move)).Scan(&groupID); err != nil {
				return fmt.Errorf("create group: %w", err)
			}
			if _, err := exec.ExecContext(ctx, moveGroupSQL, groupID, moveType, pq.Array(move)); err != nil {
				return fmt.Errorf("move postings: %w", err)
			}
			touched[groupID.String()] = true
			moved += len(move)
		}

		ids := make([]string, 0, len(touched))
		for id := range touched {
			ids = append(ids, id)
		}
		if _, err := exec.ExecContext(ctx, dropResidualsSQL, pq.Array(ids)); err != nil {
			return fmt.Errorf("drop residuals: %w", err)
		}
		// A group the regroup emptied goes with its residuals, and one that kept
		// something is re-dated to what it has left, exactly as a period replace
		// leaves them.
		if _, err := exec.ExecContext(ctx, deleteEmptiedGroupsSQL, pq.Array(ids)); err != nil {
			return fmt.Errorf("delete emptied groups: %w", err)
		}
		if _, err := exec.ExecContext(ctx, repointGroupTimestampsSQL, pq.Array(ids)); err != nil {
			return fmt.Errorf("repoint group timestamps: %w", err)
		}
		// The matches naming a group whose membership moved are cache, and the
		// matcher rebuilds them from the same evidence on its next cycle.
		if _, err := exec.ExecContext(ctx, deleteTouchedMatchesSQL, pq.Array(ids)); err != nil {
			return fmt.Errorf("delete touched matches: %w", err)
		}
		return routeSurvivors(ctx, exec, userUUID, pq.Array(ids))
	})
	return moved, err
}
