package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"

	"github.com/leedenison/portfoliodb/server/db"
)

// ensureListing gives an instrument the listing it must have: a security has at
// least one currency line, and everything downstream of this level leans on
// that. It either mints one or moves the security's unknown listing onto a
// currency it has just learned.
//
// currency == "" means the currency is unknown, which is a different claim from
// "this security has one line and it is X". The unknown listing is never
// priceable and never event-bearing, and moving it onto a currency later is a
// relabelling rather than a loss, which is what makes the split in adr/0071
// possible.
//
// Idempotent, and safe to call for an instrument that already has listings: a
// security that already holds this currency family keeps the listing it has,
// including the code it is quoted in. GBX does not become GBP.
func ensureListing(ctx context.Context, exec queryable, instrumentID uuid.UUID, currency string) error {
	if currency == "" {
		// Nothing to learn, so this only fills a gap. A security that already
		// has a currency line is not given an unknown one beside it: the two
		// would say contradictory things about how many lines it has.
		_, err := exec.ExecContext(ctx, `
			INSERT INTO instrument_listings (instrument_id, currency)
			SELECT $1, NULL
			WHERE NOT EXISTS (SELECT 1 FROM instrument_listings WHERE instrument_id = $1)
		`, instrumentID)
		if err != nil {
			return fmt.Errorf("ensure unknown listing: %w", err)
		}
		return nil
	}
	// A currency arriving for a security whose line was unknown names that line.
	// The guard covers the case the unique index would otherwise reject: a
	// security holding both an unknown listing and one already in this family,
	// which nothing writes today but which this must not turn into an error.
	_, err := exec.ExecContext(ctx, `
		UPDATE instrument_listings SET currency = $2
		WHERE instrument_id = $1 AND currency IS NULL
		  AND NOT EXISTS (
		    SELECT 1 FROM instrument_listings o
		    WHERE o.instrument_id = $1 AND o.currency IS NOT NULL
		      AND currency_family(o.currency) = currency_family($2)
		  )
	`, instrumentID, currency)
	if err != nil {
		return fmt.Errorf("name unknown listing: %w", err)
	}
	// ON CONFLICT infers the partial index by restating its predicate, so a
	// security already quoted in this family is a no-op rather than an error --
	// including when a provider states GBP for a line already stored in GBX.
	_, err = exec.ExecContext(ctx, `
		INSERT INTO instrument_listings (instrument_id, currency)
		VALUES ($1, $2)
		ON CONFLICT (instrument_id, currency_family(currency)) WHERE currency IS NOT NULL
		DO NOTHING
	`, instrumentID, currency)
	if err != nil {
		return fmt.Errorf("ensure listing %s: %w", currency, err)
	}
	return nil
}

// ListingsByInstrument implements db.ListingDB.
func (p *Postgres) ListingsByInstrument(ctx context.Context, instrumentIDs []string) (map[string][]*db.Listing, error) {
	out := make(map[string][]*db.Listing, len(instrumentIDs))
	if len(instrumentIDs) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, 0, len(instrumentIDs))
	for _, s := range instrumentIDs {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("listings by instrument: invalid id %q: %w", s, err)
		}
		ids = append(ids, id)
	}
	listings, err := queryListings(ctx, p.q, ids)
	if err != nil {
		return nil, err
	}
	for _, l := range listings {
		out[l.InstrumentID] = append(out[l.InstrumentID], l)
	}
	return out, nil
}

// loadListings batch-loads listings for the given instrument IDs and attaches
// them to the corresponding rows, in the pattern loadIdentifiers follows.
func loadListings(ctx context.Context, q queryable, ids []uuid.UUID, rows []*db.InstrumentRow) error {
	if len(ids) == 0 {
		return nil
	}
	listings, err := queryListings(ctx, q, ids)
	if err != nil {
		return err
	}
	byID := make(map[string]*db.InstrumentRow, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	for _, l := range listings {
		if r := byID[l.InstrumentID]; r != nil {
			r.Listings = append(r.Listings, l)
		}
	}
	return nil
}

// queryListings reads the listings of the given instruments in a stable order:
// a security's known lines first, by currency, then its unknown one. Nothing
// makes a listing primary -- naming one would reintroduce the
// default-versus-unknown conflation this level removes -- so the order is for
// reproducibility and not a ranking.
func queryListings(ctx context.Context, q queryable, ids []uuid.UUID) ([]*db.Listing, error) {
	inClause, args := inClauseUUIDs(ids)
	rows, err := q.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, instrument_id, currency, valid_from, valid_before, created_at
		FROM instrument_listings
		WHERE instrument_id IN (%s)
		ORDER BY instrument_id, currency IS NULL, currency
	`, inClause), args...)
	if err != nil {
		return nil, fmt.Errorf("query listings: %w", err)
	}
	defer rows.Close()
	var out []*db.Listing
	for rows.Next() {
		var id, instID uuid.UUID
		var currency sql.NullString
		var validFrom, validBefore sql.NullTime
		l := &db.Listing{}
		if err := rows.Scan(&id, &instID, &currency, &validFrom, &validBefore, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan listing: %w", err)
		}
		l.ID = id.String()
		l.InstrumentID = instID.String()
		if currency.Valid {
			l.Currency = &currency.String
		}
		if validFrom.Valid {
			l.ValidFrom = &validFrom.Time
		}
		if validBefore.Valid {
			l.ValidBefore = &validBefore.Time
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
