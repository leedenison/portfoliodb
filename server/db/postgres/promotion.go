package postgres

import (
	"context"
	"fmt"

	"github.com/leedenison/portfoliodb/server/db"
)

// The promotion sweep: what turns an identity claim enough users hold into a
// fact the instance holds.
//
// A mapping here is a triple and the instrument it names. Users agree when they
// hold the same triple against the same instrument; they conflict when they hold
// it against different ones. Agreement is what is promoted, and it is counted in
// distinct owners rather than in rows: one user with two brokers writing one
// description is one user, and a threshold that counted rows would promote on
// their word alone.
//
// What the threshold measures is the channel and not the claim. Every user reads
// the same mapping out of the same broker security master, so their agreement
// says nothing about whether the broker is right -- only that the file was not
// doctored, stale or from the wrong account. See
// docs/adr/0063-identity-claims-are-owned-until-users-corroborate-them.md.

// candidate is one mapping the sweep is deciding about.
type candidate struct {
	Type       string  `db:"identifier_type"`
	Domain     *string `db:"domain"`
	Value      string  `db:"value"`
	Instrument string  `db:"instrument_id"`
	Owners     int     `db:"owners"`
	// ValidFrom and ValidBefore are the union of the intervals the promoted rows
	// were written with. They overlap by construction -- a triple two owners hold
	// over disjoint intervals is two mappings, not one -- so the union is
	// contiguous and a NULL bound on either side absorbs the rest.
	ValidFrom   *string `db:"valid_from"`
	ValidBefore *string `db:"valid_before"`
}

// PromoteCorroboratedIdentifiers implements db.PromotionDB.
func (p *Postgres) PromoteCorroboratedIdentifiers(ctx context.Context, threshold int) (db.PromotionResult, error) {
	var res db.PromotionResult
	if threshold < 1 {
		return res, fmt.Errorf("promotion threshold is %d, want at least 1", threshold)
	}
	err := p.runInTx(ctx, func(exec queryable) error {
		var cands []candidate
		if err := exec.SelectContext(ctx, &cands, promotionCandidates, threshold); err != nil {
			return fmt.Errorf("list promotion candidates: %w", err)
		}
		for _, c := range cands {
			if err := p.promote(ctx, exec, c, &res); err != nil {
				return err
			}
		}
		return nil
	})
	return res, err
}

// promotionCandidates is every mapping enough users hold, with nobody holding a
// mapping that contradicts it.
//
// The contradiction test is the whole of the work. A group is a triple and an
// instrument; it is disqualified where any user-owned row holds that triple
// against a different instrument over an overlapping interval, and equally where
// a system row does -- the second is the instance and one of its users
// disagreeing, which is a person's decision rather than a sweep's, and promoting
// it would fail the exclusion constraint besides.
//
// A system row naming the same instrument does not disqualify anything: it
// agrees, so the user rows under it are already corroborated and the promote
// below deletes them without writing a second row.
//
// Rows are locked as they are read. Two sweeps cannot run at once today -- the
// worker is serial -- but a promotion deletes rows an ingestion may be resolving
// through, and taking them in one order stops the two deadlocking.
const promotionCandidates = `
	WITH owned AS (
		SELECT id, instrument_id, identifier_type, COALESCE(domain, '') AS dom, domain, value,
			valid_from, valid_before, owner
		FROM instrument_identifiers
		WHERE owner IS NOT NULL
	), grouped AS (
		SELECT o.identifier_type, o.dom, o.domain, o.value, o.instrument_id,
			count(DISTINCT o.owner) AS owners,
			min(o.valid_from) FILTER (WHERE o.valid_from IS NOT NULL) AS from_bound,
			bool_or(o.valid_from IS NULL) AS unbounded_from,
			max(o.valid_before) FILTER (WHERE o.valid_before IS NOT NULL) AS before_bound,
			bool_or(o.valid_before IS NULL) AS unbounded_before
		FROM owned o
		GROUP BY o.identifier_type, o.dom, o.domain, o.value, o.instrument_id
	)
	SELECT g.identifier_type, g.domain, g.value, g.instrument_id, g.owners,
		CASE WHEN g.unbounded_from THEN NULL ELSE to_char(g.from_bound, 'YYYY-MM-DD') END AS valid_from,
		CASE WHEN g.unbounded_before THEN NULL ELSE to_char(g.before_bound, 'YYYY-MM-DD') END AS valid_before
	FROM grouped g
	WHERE g.owners >= $1
	AND NOT EXISTS (
		SELECT 1 FROM instrument_identifiers x
		WHERE x.identifier_type = g.identifier_type
		AND COALESCE(x.domain, '') = g.dom
		AND x.value = g.value
		AND x.instrument_id <> g.instrument_id
		AND daterange(x.valid_from, x.valid_before) && daterange(
				CASE WHEN g.unbounded_from THEN NULL ELSE g.from_bound END,
				CASE WHEN g.unbounded_before THEN NULL ELSE g.before_bound END)
	)
	ORDER BY g.identifier_type, g.dom, g.value
`

// promote makes one mapping the instance's and deletes the claims it was
// promoted from.
//
// Where a system row already names the same instrument the claims are deleted
// and nothing is written: the mapping is a fact already, and a second row for
// one triple and one owner is what the exclusion constraint forbids. That is the
// ordinary case for a description a plugin later confirmed, and it is how a
// claim stops taking up a slot once it has been overtaken.
func (p *Postgres) promote(ctx context.Context, exec queryable, c candidate, res *db.PromotionResult) error {
	var existing int
	err := exec.QueryRowContext(ctx, `
		SELECT count(*) FROM instrument_identifiers
		WHERE identifier_type = $1 AND COALESCE(domain, '') = COALESCE($2, '') AND value = $3
		AND instrument_id = $4 AND owner IS NULL
		AND daterange(valid_from, valid_before) && daterange($5::date, $6::date)
	`, c.Type, c.Domain, c.Value, c.Instrument, c.ValidFrom, c.ValidBefore).Scan(&existing)
	if err != nil {
		return fmt.Errorf("check for an existing fact (%s/%s): %w", c.Type, c.Value, err)
	}
	if existing == 0 {
		// canonical is read off the row it is promoted from: what the name is
		// good for does not change when who vouches for it does.
		_, err := exec.ExecContext(ctx, `
			INSERT INTO instrument_identifiers
				(instrument_id, identifier_type, domain, value, canonical, owner, valid_from, valid_before)
			SELECT $4, $1, $2, $3, bool_or(canonical), NULL, $5::date, $6::date
			FROM instrument_identifiers
			WHERE identifier_type = $1 AND COALESCE(domain, '') = COALESCE($2, '') AND value = $3
			AND instrument_id = $4 AND owner IS NOT NULL
		`, c.Type, c.Domain, c.Value, c.Instrument, c.ValidFrom, c.ValidBefore)
		if err != nil {
			return fmt.Errorf("promote (%s/%s): %w", c.Type, c.Value, err)
		}
		res.Promoted++
	} else {
		res.AlreadyHeld++
	}
	r, err := exec.ExecContext(ctx, `
		DELETE FROM instrument_identifiers
		WHERE identifier_type = $1 AND COALESCE(domain, '') = COALESCE($2, '') AND value = $3
		AND instrument_id = $4 AND owner IS NOT NULL
	`, c.Type, c.Domain, c.Value, c.Instrument)
	if err != nil {
		return fmt.Errorf("delete promoted claims (%s/%s): %w", c.Type, c.Value, err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	res.ClaimsCleared += int(n)
	return nil
}

// CountUnpromotableClaims implements db.PromotionDB.
//
// A mapping more than one user holds with more than one answer. Nothing is done
// about it here: both rows stay in place, each still resolving for its own
// owner, and resolving it is a person's decision on the surface 0168 builds.
// Counting it is what says the sweep is leaving work behind rather than having
// none.
func (p *Postgres) CountUnpromotableClaims(ctx context.Context) (int, error) {
	var n int
	err := p.q.QueryRowContext(ctx, `
		SELECT count(*) FROM (
			SELECT identifier_type, COALESCE(domain, '') AS dom, value
			FROM instrument_identifiers
			WHERE owner IS NOT NULL
			GROUP BY identifier_type, COALESCE(domain, ''), value
			HAVING count(DISTINCT instrument_id) > 1
		) contested
	`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count unpromotable claims: %w", err)
	}
	return n, nil
}
