package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Coverage tables share a shape: (instrument_id, plugin_id, covered_from,
// covered_before, last_fetched_at) with the half-open [covered_from,
// covered_before) convention and non-overlapping spans per (instrument,
// plugin). price_coverage and corporate_event_coverage both use it, so the
// merge below is written once and named by table.
const (
	priceCoverageTable          = "price_coverage"
	corporateEventCoverageTable = "corporate_event_coverage"
)

// upsertCoverageSpan merges [from, before) into the coverage spans already
// recorded for (instrumentID, pluginID) in table. Every existing row whose
// interval touches the new one is deleted and replaced by a single row spanning
// the union, so the table never holds overlapping or abutting spans.
//
// The merged row keeps the oldest constituent last_fetched_at, since the union
// is only as freshly confirmed as its stalest part. lastFetchedAt is when the
// supplied span was confirmed; nil means now.
//
// exec must be a transaction: the compute, delete and insert are three
// statements and are only safe together.
func upsertCoverageSpan(ctx context.Context, exec queryable, table, instrumentID, pluginID string, from, before time.Time, lastFetchedAt *time.Time) error {
	if !before.After(from) {
		return fmt.Errorf("upsert %s: covered_before %s not after covered_from %s", table, before, from)
	}
	id, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("upsert %s: invalid instrument id %q: %w", table, instrumentID, err)
	}

	// Half-open [a,b) and [c,d) touch or overlap iff a <= d AND c <= b, so
	// abutting spans merge with no adjacency fudge. CTE data-modifying
	// statements share a snapshot in PostgreSQL so DELETE and INSERT cannot see
	// one another -- run them as separate statements inside the transaction.
	//
	// LEAST ignores NULL, so with no touching rows the merged fetch time is just
	// the supplied one (or now()).
	touching := `
		WHERE instrument_id = $1
		  AND plugin_id     = $2
		  AND covered_from   <= $4::date
		  AND covered_before >= $3::date`

	var newFrom, newBefore, newFetched time.Time
	err = exec.QueryRowContext(ctx, `
		SELECT
			LEAST($3::date,    COALESCE(MIN(covered_from),   $3::date)),
			GREATEST($4::date, COALESCE(MAX(covered_before), $4::date)),
			LEAST(COALESCE($5::timestamptz, now()), MIN(last_fetched_at))
		FROM `+table+touching,
		id, pluginID, from, before, lastFetchedAt).Scan(&newFrom, &newBefore, &newFetched)
	if err != nil {
		return fmt.Errorf("upsert %s: compute merge: %w", table, err)
	}
	if _, err := exec.ExecContext(ctx, `DELETE FROM `+table+touching, id, pluginID, from, before); err != nil {
		return fmt.Errorf("upsert %s: delete overlapping: %w", table, err)
	}
	if _, err := exec.ExecContext(ctx, `
		INSERT INTO `+table+` (instrument_id, plugin_id, covered_from, covered_before, last_fetched_at)
		VALUES ($1, $2, $3, $4, $5)
	`, id, pluginID, newFrom, newBefore, newFetched); err != nil {
		return fmt.Errorf("upsert %s: insert merged: %w", table, err)
	}
	return nil
}
