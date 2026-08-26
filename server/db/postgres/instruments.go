package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"
)

// errIdentifierExists is returned when EnsureInstrument hits a unique violation (identifier already for another instrument).
var errIdentifierExists = errors.New("identifier already exists for another instrument")

// splitByGrain divides identifiers into those naming the security and those
// naming one of its listings, which is the same thing as dividing them by which
// table they are stored in. The declared grain of the type decides it, so a
// caller never inspects a domain to work out what a value is about.
func splitByGrain(ids []db.IdentifierInput) (security, listing []db.IdentifierInput) {
	for _, idn := range ids {
		if identifier.NamesAListing(idn.Ref.Type) {
			listing = append(listing, idn)
		} else {
			security = append(security, idn)
		}
	}
	return security, listing
}

// splitProviderByGrain is splitByGrain for provider-specific identifiers, whose
// grain comes from a table of its own: a provider type is a free-form string
// rather than a vocabulary member, and an undeclared one names the security.
func splitProviderByGrain(ids []db.ProviderIdentifierInput) (security, listing []db.ProviderIdentifierInput) {
	for _, pi := range ids {
		if identifier.ProviderNamesAListing(pi.Type) {
			listing = append(listing, pi)
		} else {
			security = append(security, pi)
		}
	}
	return security, listing
}

// mergeInstruments merges mergedAway into survivor inside the same transaction: updates all txs pointing at mergedAway to survivor, moves identifier rows to survivor (or keeps survivor's if duplicate), then deletes mergedAway. exec must be a transaction.
// The delete is deliberate and lossy: mergedAway's canonical fields and its cascaded prices, splits, dividends and coverage rows go with it, and nothing records the prior identity. See docs/adr/0004-instrument-resolution-and-merge.md.
//
// mergedAway's listing rows cascade away with it, so the listing sets are unioned
// by currency family first: the survivor is given a line in every family the loser
// holds, and everything hanging off the loser's line -- its postings, names,
// prices, coverage, fetch blocks, dividends and declarations -- moves on to it.
// Two lines of one currency are one line, so a collision is a merge and there is
// no case where nothing says which of two survives. See
// docs/adr/0071-listings-merge-by-currency-and-an-unknown-one-splits.md.
func mergeInstruments(ctx context.Context, exec queryable, survivor, mergedAway uuid.UUID) error {
	if survivor == mergedAway {
		return nil
	}
	// The loser's listings cascade away with the delete below, so the postings on
	// them are moved onto the survivor's line of the same currency family first.
	// Every line has a currency and the survivor is given one in each family the
	// loser holds, so every posting has a line to move to. See
	// docs/adr/0072-a-posting-names-a-security-and-a-line.md.
	from, to, err := listingMap(ctx, exec, survivor, mergedAway)
	if err != nil {
		return err
	}
	// weight_commodity moves with instrument_id, in the same statement: a posting
	// weighing in its own security names it by instrument, so leaving the name behind
	// would split one commodity into two and unbalance the group. Only the 'inst:'
	// form needs rewriting -- a converted or cash leg is named by its currency code,
	// which the merge does not change. Both legs of a same-instrument group move
	// together, so the group stays balanced across the merge. See
	// docs/adr/0029-posting-weight-is-stored.md.
	//
	// The line moves in the same statement for the same reason the name does: the
	// three say one thing about the posting and a reader that saw them apart would
	// see a line belonging to a security the posting no longer names.
	if _, err := exec.ExecContext(ctx, `
		UPDATE txs
		SET instrument_id = $1::uuid,
		    listing_id = (SELECT m.to_id
		                  FROM unnest($3::uuid[], $4::uuid[]) AS m(from_id, to_id)
		                  WHERE m.from_id = txs.listing_id),
		    -- listing_id stays null where it was null: a posting that named no
		    -- line still names none, and the security it names has moved.
		    weight_commodity = CASE WHEN weight_commodity = 'inst:' || $2::uuid::text
		                            THEN 'inst:' || $1::uuid::text
		                            ELSE weight_commodity END
		WHERE instrument_id = $2::uuid
	`, survivor, mergedAway, pq.Array(from), pq.Array(to)); err != nil {
		return fmt.Errorf("update txs: %w", err)
	}
	// A dividend is stored against a line, so the loser's would cascade away with
	// its listings. It moves onto the survivor's line of the same currency family
	// for the same reason the postings do -- it is a fact about the payment, not
	// about which of two duplicate rows recorded the security.
	//
	// The survivor's row wins a collision: both describe one payment, and the
	// loser's is the copy made while the duplicate existed. The delete has to run
	// first, because (listing_id, ex_date) is the primary key and the update would
	// otherwise violate it.
	if _, err := exec.ExecContext(ctx, `
		DELETE FROM cash_dividends d
		USING unnest($1::uuid[], $2::uuid[]) AS m(from_id, to_id)
		WHERE d.listing_id = m.from_id
		  AND EXISTS (SELECT 1 FROM cash_dividends s
		              WHERE s.listing_id = m.to_id AND s.ex_date = d.ex_date)
	`, pq.Array(from), pq.Array(to)); err != nil {
		return fmt.Errorf("drop superseded dividends: %w", err)
	}
	if _, err := exec.ExecContext(ctx, `
		UPDATE cash_dividends d SET listing_id = m.to_id
		FROM unnest($1::uuid[], $2::uuid[]) AS m(from_id, to_id)
		WHERE d.listing_id = m.from_id
	`, pq.Array(from), pq.Array(to)); err != nil {
		return fmt.Errorf("update cash dividends: %w", err)
	}
	// A transfer match is keyed on the commodity in flight, so it moves with the
	// postings it links. Left behind it would point at an instrument the delete
	// below is about to remove, and the pair would look unmatched again.
	//
	// The two lines move in the same statement, over the same pairing, for the
	// reason a posting's does: the three say one thing about where the value went,
	// and their foreign keys have no ON DELETE, so a line left behind would fail
	// the merge outright. A null stays null -- a side that named no line still
	// names none.
	//
	// This can in principle collide with idx_transfer_matches_from/_to, which are
	// unique per (group, instrument): one group would have to hold matched
	// residuals in both instruments being merged, which needs a security transfer group whose
	// two securities turn out to be the same one. No converter emits that. It is
	// left to fail the merge loudly rather than resolved with ON CONFLICT DO
	// NOTHING, because silently dropping one of the two links would leave a side
	// unmatched with nothing to say why -- and a merge that aborts is recoverable,
	// where a quietly wrong ledger is not.
	if _, err := exec.ExecContext(ctx, `
		UPDATE transfer_matches
		SET instrument_id   = $1::uuid,
		    from_listing_id = (SELECT m.to_id
		                       FROM unnest($3::uuid[], $4::uuid[]) AS m(from_id, to_id)
		                       WHERE m.from_id = transfer_matches.from_listing_id),
		    to_listing_id   = (SELECT m.to_id
		                       FROM unnest($3::uuid[], $4::uuid[]) AS m(from_id, to_id)
		                       WHERE m.from_id = transfer_matches.to_listing_id)
		WHERE instrument_id = $2::uuid
	`, survivor, mergedAway, pq.Array(from), pq.Array(to)); err != nil {
		return fmt.Errorf("update transfer matches: %w", err)
	}
	// The validity interval moves with the name. A name the loser wore and gave
	// up is history the survivor inherits, and dropping the bounds here would
	// re-open it.
	rows, err := exec.QueryContext(ctx, `SELECT identifier_type, domain, value, canonical, valid_from, valid_before FROM instrument_identifiers WHERE instrument_id = $1`, mergedAway)
	if err != nil {
		return fmt.Errorf("list identifiers: %w", err)
	}
	defer rows.Close()
	var toInsert []db.IdentifierInput
	for rows.Next() {
		var idn db.IdentifierInput
		var domain sql.NullString
		var validFrom, validBefore sql.NullTime
		if err := rows.Scan(&idn.Ref.Type, &domain, &idn.Ref.Value, &idn.Canonical, &validFrom, &validBefore); err != nil {
			return err
		}
		if domain.Valid {
			idn.Ref.Domain = domain.String
		}
		if validFrom.Valid {
			idn.ValidFrom = &validFrom.Time
		}
		if validBefore.Valid {
			idn.ValidBefore = &validBefore.Time
		}
		toInsert = append(toInsert, idn)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := exec.ExecContext(ctx, `DELETE FROM instrument_identifiers WHERE instrument_id = $1`, mergedAway); err != nil {
		return fmt.Errorf("delete merged identifiers: %w", err)
	}
	for _, idn := range toInsert {
		_, err := exec.ExecContext(ctx, `
			INSERT INTO instrument_identifiers (instrument_id, identifier_type, domain, value, canonical, valid_from, valid_before) VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, survivor, idn.Ref.Type, nullStr(idn.Ref.Domain), idn.Ref.Value, idn.Canonical, nullTime(idn.ValidFrom), nullTime(idn.ValidBefore))
		if err != nil {
			if isIdentifierConflict(err) {
				continue
			}
			return fmt.Errorf("insert identifier: %w", err)
		}
	}
	if err := mergeListingIdentifiers(ctx, exec, survivor, from, to); err != nil {
		return err
	}
	// The loser's names that name no line move by security alone: they are on no
	// listing, so the pairing above has nothing to say about them, and they would
	// cascade away with the loser. A name the survivor already holds fails the
	// overlap constraint, and the survivor holding it already is the two rows
	// saying the same thing, so it is dropped rather than failing the merge --
	// the same judgement mergeListingIdentifiers makes.
	for _, tbl := range []string{"instrument_listing_identifiers", "provider_listing_identifiers"} {
		if _, err := exec.ExecContext(ctx, fmt.Sprintf(`
			UPDATE %s SET instrument_id = $1
			WHERE instrument_id = $2 AND listing_id IS NULL
			  AND NOT EXISTS (
			    SELECT 1 FROM %s o
			    WHERE o.instrument_id = $1 AND o.identifier_type = %s.identifier_type
			      AND o.value = %s.value AND COALESCE(o.domain, '') = COALESCE(%s.domain, '')
			  )
		`, tbl, tbl, tbl, tbl, tbl), survivor, mergedAway); err != nil {
			return fmt.Errorf("move unplaced names from %s: %w", tbl, err)
		}
	}
	if err := mergeListingContents(ctx, exec, from, to); err != nil {
		return err
	}
	// A declaration is a statement about a holding, so it moves with the postings
	// it describes and on to the same line. Left behind it would point at an
	// instrument the delete below removes, and its foreign key has no ON DELETE,
	// so the merge would fail outright rather than quietly cascade.
	//
	// The survivor's row wins a collision, as the dividend above does: two
	// declarations of one holding at one date are the same statement made twice
	// while the duplicate existed. The delete runs first because the partial
	// unique indexes would otherwise reject the update.
	//
	// IS NOT DISTINCT FROM, because the line the loser's row is moving to is null
	// where it named none: a declaration that could not say which line stays on
	// none, exactly as a posting does, and two of those are still the same
	// statement twice.
	if _, err := exec.ExecContext(ctx, `
		DELETE FROM holding_declarations d
		WHERE d.instrument_id = $2::uuid
		  AND EXISTS (
		    SELECT 1 FROM holding_declarations s
		    WHERE s.instrument_id = $1::uuid AND s.user_id = d.user_id
		      AND s.broker = d.broker AND s.account = d.account
		      AND s.as_of_date = d.as_of_date
		      AND s.listing_id IS NOT DISTINCT FROM (
		        SELECT m.to_id FROM unnest($3::uuid[], $4::uuid[]) AS m(from_id, to_id)
		        WHERE m.from_id = d.listing_id)
		  )
	`, survivor, mergedAway, pq.Array(from), pq.Array(to)); err != nil {
		return fmt.Errorf("drop superseded declarations: %w", err)
	}
	if _, err := exec.ExecContext(ctx, `
		UPDATE holding_declarations
		SET instrument_id = $1::uuid,
		    listing_id = (SELECT m.to_id
		                  FROM unnest($3::uuid[], $4::uuid[]) AS m(from_id, to_id)
		                  WHERE m.from_id = holding_declarations.listing_id)
		WHERE instrument_id = $2::uuid
	`, survivor, mergedAway, pq.Array(from), pq.Array(to)); err != nil {
		return fmt.Errorf("update holding declarations: %w", err)
	}
	// Update any instruments that referenced mergedAway as their underlying.
	// A derivative names a line of its underlying, so the loser's derivatives move
	// over the same pairing the postings and dividends use rather than by
	// instrument id. Without it the loser's listings cascade away under a
	// reference the FK will not let go.
	//
	// Every line has a target, so no derivative is left pointing at a line
	// the delete is about to remove.
	if _, err := exec.ExecContext(ctx, `
		UPDATE instruments i SET underlying_listing_id = m.to_id
		FROM unnest($1::uuid[], $2::uuid[]) AS m(from_id, to_id)
		WHERE i.underlying_listing_id = m.from_id
	`, pq.Array(from), pq.Array(to)); err != nil {
		return fmt.Errorf("update instruments.underlying_listing_id: %w", err)
	}
	if _, err := exec.ExecContext(ctx, `DELETE FROM instruments WHERE id = $1`, mergedAway); err != nil {
		return fmt.Errorf("delete merged instrument: %w", err)
	}
	return nil
}

// listingMap pairs each of the loser's listings with the survivor's line of the
// same currency family, minting one where the survivor has no line in that family
// yet, as two parallel arrays. It is worked out once and used twice -- for the
// postings on a line and for the names on it -- so the two cannot disagree.
//
// The family and not the code, so a line stored in GBX and one stored in GBP are
// one line and everything on them ends up together, which is the whole reason
// listing uniqueness is on the family.
//
// Every line carries a currency and ensureListing mints one where the survivor
// has none in that family, so every one of the loser's lines has a target: the
// pairing is total, and the merge never has to decide what to do with the rows on
// a line it could not place.
func listingMap(ctx context.Context, exec queryable, survivor, mergedAway uuid.UUID) ([]uuid.UUID, []uuid.UUID, error) {
	rows, err := exec.QueryContext(ctx, `
		SELECT id, currency FROM instrument_listings WHERE instrument_id = $1
	`, mergedAway)
	if err != nil {
		return nil, nil, fmt.Errorf("list merged listings: %w", err)
	}
	defer rows.Close()
	var from []uuid.UUID
	var currencies []string
	for rows.Next() {
		var id uuid.UUID
		var code string
		if err := rows.Scan(&id, &code); err != nil {
			return nil, nil, fmt.Errorf("scan merged listing: %w", err)
		}
		from = append(from, id)
		currencies = append(currencies, code)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	to := make([]uuid.UUID, len(from))
	for i, code := range currencies {
		target, err := ensureListing(ctx, exec, survivor, code)
		if err != nil {
			return nil, nil, fmt.Errorf("merge listing %s: %w", from[i], err)
		}
		to[i] = target
	}
	return from, to, nil
}

// mergeListingIdentifiers moves the names on each of mergedAway's listings onto
// the survivor's line of the same currency family, over the pairing listingMap
// worked out for the postings -- so the names and the postings on a line end up in
// the same place, which two independent walks of the listing set could not promise.
//
// Delete-then-insert rather than an UPDATE of listing_id, for the reason the
// security-grain move above has it: the overlap constraint is global, so a name
// the survivor already holds over the same interval would fail the merge, and
// skipping it is right -- the survivor holding it already is the two rows saying
// the same thing.
func mergeListingIdentifiers(ctx context.Context, exec queryable, survivor uuid.UUID, from []uuid.UUID, to []uuid.UUID) error {
	for i, source := range from {
		target := to[i]
		idns, err := readListingIdentifiers(ctx, exec, source)
		if err != nil {
			return err
		}
		if _, err := exec.ExecContext(ctx, `DELETE FROM instrument_listing_identifiers WHERE listing_id = $1`, source); err != nil {
			return fmt.Errorf("delete merged listing identifiers: %w", err)
		}
		for _, idn := range idns {
			_, err := exec.ExecContext(ctx, `
				INSERT INTO instrument_listing_identifiers (instrument_id, listing_id, identifier_type, domain, value, canonical, valid_from, valid_before)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			`, survivor, target, idn.Ref.Type, nullStr(idn.Ref.Domain), idn.Ref.Value, idn.Canonical, nullTime(idn.ValidFrom), nullTime(idn.ValidBefore))
			if err != nil {
				if isIdentifierConflict(err) {
					continue
				}
				return fmt.Errorf("insert listing identifier: %w", err)
			}
		}
	}
	return nil
}

// mergeListingContents moves what hangs off each of the loser's lines on to the
// survivor's line of the same currency family: the prices quoted for it, the
// spans a plugin has answered for, the plugins that refuse to answer, and the
// provider-scoped names it is addressed by. Without this they cascade away with
// the loser and a merge loses history it will only get back by re-fetching, which
// costs quota for data nobody doubted. See
// docs/adr/0071-listings-merge-by-currency-and-an-unknown-one-splits.md.
//
// The survivor's row wins every collision, as the dividend move above has it:
// two rows describing one thing on one line are one row, and the loser's is the
// copy made while the duplicate existed. Each delete-then-update pair runs in that
// order because the primary key would otherwise reject the update.
//
// Coverage is the exception and goes through upsertCoverageSpan, because two spans
// of one line are not duplicates to be dropped: [Jan, Mar) and [Feb, Jun) are one
// answer covering [Jan, Jun), and keeping only the survivor's would silently
// un-fetch March to June.
func mergeListingContents(ctx context.Context, exec queryable, from, to []uuid.UUID) error {
	for _, t := range []struct{ name, key string }{
		{"eod_prices", "price_date"},
		{"price_fetch_blocks", "plugin_id"},
	} {
		if _, err := exec.ExecContext(ctx, fmt.Sprintf(`
			DELETE FROM %[1]s r
			USING unnest($1::uuid[], $2::uuid[]) AS m(from_id, to_id)
			WHERE r.listing_id = m.from_id
			  AND EXISTS (SELECT 1 FROM %[1]s s
			              WHERE s.listing_id = m.to_id AND s.%[2]s = r.%[2]s)
		`, t.name, t.key), pq.Array(from), pq.Array(to)); err != nil {
			return fmt.Errorf("drop superseded %s: %w", t.name, err)
		}
		if _, err := exec.ExecContext(ctx, fmt.Sprintf(`
			UPDATE %s r SET listing_id = m.to_id
			FROM unnest($1::uuid[], $2::uuid[]) AS m(from_id, to_id)
			WHERE r.listing_id = m.from_id
		`, t.name), pq.Array(from), pq.Array(to)); err != nil {
			return fmt.Errorf("update %s: %w", t.name, err)
		}
	}
	// A provider name the survivor's line already holds is the two rows saying the
	// same thing, so it is left to cascade rather than moved. Guarded rather than
	// deleted first, because these rows carry no interval and nothing distinguishes
	// the copies.
	if _, err := exec.ExecContext(ctx, `
		UPDATE provider_listing_identifiers r
		SET instrument_id = l.instrument_id, listing_id = m.to_id
		FROM unnest($1::uuid[], $2::uuid[]) AS m(from_id, to_id)
		JOIN instrument_listings l ON l.id = m.to_id
		WHERE r.listing_id = m.from_id
		  AND NOT EXISTS (
		    SELECT 1 FROM provider_listing_identifiers s
		    WHERE s.listing_id = m.to_id AND s.provider = r.provider
		      AND s.identifier_type = r.identifier_type AND s.value = r.value
		      AND COALESCE(s.domain, '') = COALESCE(r.domain, '')
		  )
	`, pq.Array(from), pq.Array(to)); err != nil {
		return fmt.Errorf("update provider listing identifiers: %w", err)
	}
	return mergeCoverage(ctx, exec, from, to)
}

// mergeCoverage moves each of the loser's price coverage spans on to the
// survivor's line, through the same merge an ordinary fetch records with, so a
// span that abuts or overlaps one already there becomes one span rather than two
// rows the table's own invariant forbids.
func mergeCoverage(ctx context.Context, exec queryable, from, to []uuid.UUID) error {
	type span struct {
		plugin       string
		from, before time.Time
		fetched      time.Time
	}
	for i, source := range from {
		rows, err := exec.QueryContext(ctx, `
			SELECT plugin_id, covered_from, covered_before, last_fetched_at
			FROM price_coverage WHERE listing_id = $1
		`, source)
		if err != nil {
			return fmt.Errorf("read merged coverage: %w", err)
		}
		var spans []span
		for rows.Next() {
			var sp span
			if err := rows.Scan(&sp.plugin, &sp.from, &sp.before, &sp.fetched); err != nil {
				rows.Close()
				return fmt.Errorf("scan merged coverage: %w", err)
			}
			spans = append(spans, sp)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(spans) == 0 {
			continue
		}
		if _, err := exec.ExecContext(ctx, `DELETE FROM price_coverage WHERE listing_id = $1`, source); err != nil {
			return fmt.Errorf("delete merged coverage: %w", err)
		}
		for _, sp := range spans {
			fetched := sp.fetched
			if err := upsertCoverageSpan(ctx, exec, priceCoverage, to[i].String(), sp.plugin, sp.from, sp.before, &fetched); err != nil {
				return err
			}
		}
	}
	return nil
}

// readListingIdentifiers reads one listing's identifiers, bounds included. The
// validity interval moves with the name for the reason the security-grain read
// gives: a name the loser wore and gave up is history the survivor inherits.
func readListingIdentifiers(ctx context.Context, exec queryable, listingID uuid.UUID) ([]db.IdentifierInput, error) {
	rows, err := exec.QueryContext(ctx, `
		SELECT identifier_type, domain, value, canonical, valid_from, valid_before
		FROM instrument_listing_identifiers WHERE listing_id = $1
	`, listingID)
	if err != nil {
		return nil, fmt.Errorf("list listing identifiers: %w", err)
	}
	defer rows.Close()
	var out []db.IdentifierInput
	for rows.Next() {
		var idn db.IdentifierInput
		var domain sql.NullString
		var validFrom, validBefore sql.NullTime
		if err := rows.Scan(&idn.Ref.Type, &domain, &idn.Ref.Value, &idn.Canonical, &validFrom, &validBefore); err != nil {
			return nil, err
		}
		if domain.Valid {
			idn.Ref.Domain = domain.String
		}
		if validFrom.Valid {
			idn.ValidFrom = &validFrom.Time
		}
		if validBefore.Valid {
			idn.ValidBefore = &validBefore.Time
		}
		out = append(out, idn)
	}
	return out, rows.Err()
}

// pickSurvivor returns the instrument ID that should survive when merging the given set (most identifiers, then oldest created_at). ids must have at least one element.
func pickSurvivor(ctx context.Context, q queryable, ids []uuid.UUID) (uuid.UUID, error) {
	if len(ids) == 0 {
		return uuid.Nil, fmt.Errorf("pickSurvivor requires at least one id")
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	inClause, args := inClauseUUIDs(ids)
	// Names at both grains count. The tie-break is "the row that knows the most
	// about this security", and a listing's ticker is as much a name for it as
	// an ISIN is; counting one table would make a ticker-rich instrument lose to
	// a bare one.
	query := fmt.Sprintf(`
		SELECT i.id, i.created_at,
		       (SELECT count(*) FROM instrument_identifiers WHERE instrument_id = i.id)
		     + (SELECT count(*) FROM instrument_listing_identifiers li
		        WHERE li.instrument_id = i.id) AS n
		FROM instruments i WHERE i.id IN (%s)
	`, inClause)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return uuid.Nil, fmt.Errorf("query instruments: %w", err)
	}
	defer rows.Close()
	type cand struct {
		id        uuid.UUID
		createdAt time.Time
		n         int64
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.createdAt, &c.n); err != nil {
			return uuid.Nil, err
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, err
	}
	if len(cands) == 0 {
		return uuid.Nil, fmt.Errorf("no instruments found for ids")
	}
	// Sort by n desc, created_at asc (more identifiers wins, then older wins),
	// then by id so the order is total. Two rows written in one transaction share
	// a created_at, because the column defaults to now() and that is transaction
	// time, so the first two keys can both tie; without a third the winner is
	// whatever order the rows came back in, and a merge would pick a different
	// survivor depending on the table's page layout.
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].n != cands[j].n {
			return cands[i].n > cands[j].n
		}
		if !cands[i].createdAt.Equal(cands[j].createdAt) {
			return cands[i].createdAt.Before(cands[j].createdAt)
		}
		return bytes.Compare(cands[i].id[:], cands[j].id[:]) < 0
	})
	return cands[0].id, nil
}

func isUniqueViolation(err error) bool {
	var pe *pq.Error
	return errors.As(err, &pe) && pe.Code == "23505"
}

// isIdentifierConflict reports whether err is another instrument already holding
// the identifier. instrument_identifiers states that as an exclusion constraint
// on overlapping validity rather than as a unique index, so the two codes mean
// the same thing to every caller that reacts by looking the holder up.
func isIdentifierConflict(err error) bool {
	var pe *pq.Error
	return errors.As(err, &pe) && (pe.Code == "23505" || pe.Code == "23P01")
}

// FindInstrumentByIdentifier implements db.InstrumentDB.
//
// The lookup is by value alone, over every interval: a broker file exported
// before a split names the symbol the contract traded under then, and both that
// name and the one minted for it belong to the same contract. Where retained
// history leaves more than one row, the name in force now wins and the most
// recently closed one is the fallback -- so a value two different instruments
// held over disjoint intervals resolves to the current holder. Asking what a
// value denoted on a given date is issue 0122, not this.
//
// The type says which table holds the row, so this asks one of them rather than
// searching both. A caller wanting the listing as well as the security asks
// FindListingByIdentifier directly.
//
// The query itself is findHolder's, which answers with the row's validity
// interval as well as its instrument. Merge admission needs the interval and
// this does not, and having them read one row the same way is what stops the
// two coming to disagree about which instrument a value resolves to.
func (p *Postgres) FindInstrumentByIdentifier(ctx context.Context, identifierType, domain, value string) (string, error) {
	e, ok, err := findHolder(ctx, p.q, db.InstrumentRef{Type: identifierType, Domain: domain, Value: value})
	if err != nil || !ok {
		return "", err
	}
	return e.instrument.String(), nil
}

// FindInstrumentWithMetaByIdentifier implements db.InstrumentDB. It orders by
// validity, and dispatches on grain, for the same reasons
// FindInstrumentByIdentifier does.
//
// The currencies it answers with are the lines the identifier reaches, and how
// many that is follows from the grain. A listing-grain name is on one line and
// answers with that line's currency, or with none where nobody could place it. A
// security-grain name reaches every line of the security, and answers with all of
// them: an ISIN is not quoted in a currency, the lines under it are, so the only
// honest answer to "what is this in" is the set a caller can test membership
// against.
//
// No venue, at either grain. A stated venue we do not hold is a venue we have not
// been told about rather than a disagreement, so there is nothing for a caller to
// check one against. See
// docs/adr/0077-a-venue-set-is-what-we-know-not-what-exists.md.
func (p *Postgres) FindInstrumentWithMetaByIdentifier(ctx context.Context, identifierType, domain, value string) (string, string, []string, error) {
	var id uuid.UUID
	var ac string
	var currencies []string
	var err error
	if identifier.NamesAListing(identifierType) {
		var cur string
		if domain == "" {
			err = p.q.QueryRowContext(ctx, `
				SELECT li.instrument_id, COALESCE(i.asset_class, ''), COALESCE(l.currency, '')
				FROM instrument_listing_identifiers li
				JOIN instruments i ON i.id = li.instrument_id
				LEFT JOIN instrument_listings l ON l.id = li.listing_id
				WHERE li.identifier_type = $1 AND li.domain IS NULL AND li.value = $2
				ORDER BY li.valid_before IS NULL DESC, li.valid_before DESC
				LIMIT 1
			`, identifierType, value).Scan(&id, &ac, &cur)
		} else {
			err = p.q.QueryRowContext(ctx, `
				SELECT li.instrument_id, COALESCE(i.asset_class, ''), COALESCE(l.currency, '')
				FROM instrument_listing_identifiers li
				JOIN instruments i ON i.id = li.instrument_id
				LEFT JOIN instrument_listings l ON l.id = li.listing_id
				WHERE li.identifier_type = $1 AND li.domain = $2 AND li.value = $3
				ORDER BY li.valid_before IS NULL DESC, li.valid_before DESC
				LIMIT 1
			`, identifierType, domain, value).Scan(&id, &ac, &cur)
		}
		if err == sql.ErrNoRows {
			return "", "", nil, nil
		}
		if err != nil {
			return "", "", nil, fmt.Errorf("find instrument with meta by identifier: %w", err)
		}
		if cur != "" {
			currencies = []string{cur}
		}
		return id.String(), ac, currencies, nil
	}
	// ARRAY() of the security's lines, which is empty rather than null where it
	// holds none -- a security nobody has named a line for says nothing about a
	// currency, which is not the same as saying the currency is wrong.
	const securityLines = `ARRAY(SELECT l.currency FROM instrument_listings l WHERE l.instrument_id = i.id ORDER BY l.currency)`
	if domain == "" {
		err = p.q.QueryRowContext(ctx, `
			SELECT ii.instrument_id, COALESCE(i.asset_class, ''), `+securityLines+`
			FROM instrument_identifiers ii
			JOIN instruments i ON i.id = ii.instrument_id
			WHERE ii.identifier_type = $1 AND ii.domain IS NULL AND ii.value = $2
			ORDER BY ii.valid_before IS NULL DESC, ii.valid_before DESC
			LIMIT 1
		`, identifierType, value).Scan(&id, &ac, pq.Array(&currencies))
	} else {
		err = p.q.QueryRowContext(ctx, `
			SELECT ii.instrument_id, COALESCE(i.asset_class, ''), `+securityLines+`
			FROM instrument_identifiers ii
			JOIN instruments i ON i.id = ii.instrument_id
			WHERE ii.identifier_type = $1 AND ii.domain = $2 AND ii.value = $3
			ORDER BY ii.valid_before IS NULL DESC, ii.valid_before DESC
			LIMIT 1
		`, identifierType, domain, value).Scan(&id, &ac, pq.Array(&currencies))
	}
	if err == sql.ErrNoRows {
		return "", "", nil, nil
	}
	if err != nil {
		return "", "", nil, fmt.Errorf("find instrument with meta by identifier: %w", err)
	}
	return id.String(), ac, currencies, nil
}

// FindInstrumentByTypeAndValue implements db.InstrumentDB.
// Returns "" if no row matches or if more than one instrument has the same (type, value) with different domains (ambiguous).
// FindInstrumentByTickerIgnoringSeparators implements db.InstrumentDB. The
// separator set matches identifier.NormalizeSplitTicker, and both sides are
// stripped rather than one being rewritten, because an OCC root has lost the
// separator's position and cannot have it put back.
func (p *Postgres) FindInstrumentByTickerIgnoringSeparators(ctx context.Context, value string) (string, error) {
	rows, err := p.q.QueryContext(ctx, `
		SELECT DISTINCT li.instrument_id
		FROM instrument_listing_identifiers li
		WHERE li.identifier_type = 'MIC_TICKER'
		  AND translate(li.value, './- ', '') = translate($1, './- ', '')
	`, value)
	if err != nil {
		return "", fmt.Errorf("find instrument by ticker ignoring separators: %w", err)
	}
	defer rows.Close()
	var id uuid.UUID
	var count int
	for rows.Next() {
		var next uuid.UUID
		if err := rows.Scan(&next); err != nil {
			return "", err
		}
		count++
		if count > 1 {
			return "", nil
		}
		id = next
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if count == 0 {
		return "", nil
	}
	return id.String(), nil
}

func (p *Postgres) FindInstrumentByTypeAndValue(ctx context.Context, identifierType, value string) (string, error) {
	q := `
		SELECT instrument_id FROM instrument_identifiers
		WHERE identifier_type = $1 AND value = $2
	`
	if identifier.NamesAListing(identifierType) {
		q = `
			SELECT li.instrument_id
			FROM instrument_listing_identifiers li
			WHERE li.identifier_type = $1 AND li.value = $2
		`
	}
	rows, err := p.q.QueryContext(ctx, q, identifierType, value)
	if err != nil {
		return "", fmt.Errorf("find instrument by type and value: %w", err)
	}
	defer rows.Close()
	var id uuid.UUID
	var count int
	for rows.Next() {
		var next uuid.UUID
		if err := rows.Scan(&next); err != nil {
			return "", err
		}
		count++
		if count == 1 {
			id = next
		} else if count > 1 && next != id {
			return "", nil // ambiguous: same (type, value) for different instruments
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if count == 0 {
		return "", nil
	}
	return id.String(), nil
}

// FindInstrumentBySourceDescription implements db.InstrumentDB.
// Broker descriptions are stored as identifier_type = BROKER_DESCRIPTION, domain = source, value = description.
func (p *Postgres) FindInstrumentBySourceDescription(ctx context.Context, source, description string) (string, error) {
	return p.FindInstrumentByIdentifier(ctx, "BROKER_DESCRIPTION", source, description)
}

// FindDescriptionOnlyInstrument implements db.InstrumentDB.
//
// canonical = false marks a broker description and nothing else, so holding no
// canonical identifier is the stored form of broker-description-only. The guard
// is a NOT EXISTS rather than a second round trip because the caller asks one
// question -- does this description name an instrument with no identity -- and
// two lookups would let the answer change between them.
//
// Two of them, one per grain. An instrument whose only canonical name is a
// ticker has an identity, and asking the security's table alone would report it
// as nothing but a broker's text and let a description associate it with
// something else.
//
// It orders by validity for the same reason FindInstrumentByIdentifier does.
func (p *Postgres) FindDescriptionOnlyInstrument(ctx context.Context, source, description string) (string, error) {
	var id uuid.UUID
	err := p.q.QueryRowContext(ctx, `
		SELECT ii.instrument_id FROM instrument_identifiers ii
		WHERE ii.identifier_type = 'BROKER_DESCRIPTION' AND ii.domain = $1 AND ii.value = $2
		  AND NOT EXISTS (
			SELECT 1 FROM instrument_identifiers c
			WHERE c.instrument_id = ii.instrument_id AND c.canonical
		  )
		  AND NOT EXISTS (
			SELECT 1 FROM instrument_listing_identifiers cl
			WHERE cl.instrument_id = ii.instrument_id AND cl.canonical
		  )
		ORDER BY ii.valid_before IS NULL DESC, ii.valid_before DESC
		LIMIT 1
	`, source, description).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("find description-only instrument: %w", err)
	}
	return id.String(), nil
}

// underlyingSelect names both halves of an instrument's underlying: the line it
// delivers, which is the stored column, and the security that line belongs to,
// which underlyingJoin derives. Every read of instruments carries the pair, so
// a caller that does not care which line a contract is written on is not made
// to resolve one.
const underlyingSelect = "i.underlying_listing_id, u_l.instrument_id AS underlying_id"

// underlyingJoin must precede any LATERAL that references u_l.
const underlyingJoin = "LEFT JOIN instrument_listings u_l ON u_l.id = i.underlying_listing_id"

// GetInstrument implements db.InstrumentDB.
func (p *Postgres) GetInstrument(ctx context.Context, instrumentID string) (*db.InstrumentRow, error) {
	instUUID, err := uuid.Parse(instrumentID)
	if err != nil {
		return nil, fmt.Errorf("invalid instrument id: %w", err)
	}
	var r instrumentRow
	err = p.q.GetContext(ctx, &r, `
		SELECT i.id, i.asset_class, i.name, `+underlyingSelect+`,
		       i.cik, i.sic_code,
		       i.strike, i.expiry, i.put_call, i.contract_multiplier
		FROM instruments i
		`+underlyingJoin+`
		WHERE i.id = $1
	`, instUUID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get instrument: %w", err)
	}
	row := r.toDBRow()
	instIDs := []uuid.UUID{r.ID}
	instRows := []*db.InstrumentRow{row}
	if err := loadIdentifiers(ctx, p.q, instIDs, instRows); err != nil {
		return nil, fmt.Errorf("get instrument identifiers: %w", err)
	}
	if err := loadProviderIdentifiers(ctx, p.q, instIDs, instRows); err != nil {
		return nil, fmt.Errorf("get instrument provider identifiers: %w", err)
	}
	if err := loadListings(ctx, p.q, instIDs, instRows); err != nil {
		return nil, fmt.Errorf("get instrument listings: %w", err)
	}
	return row, nil
}

// ListInstrumentsForExport implements db.InstrumentDB.
func (p *Postgres) ListInstrumentsForExport(ctx context.Context, exchangeFilter string, assetClasses []string) ([]*db.InstrumentRow, error) {
	var irows []instrumentRow
	var err error

	// No asset-class filter means every instrument, which is what a rebuild
	// needs: FX pairs are instruments, and an instrument whose asset_class is
	// still NULL -- created by a price import before identification classified
	// it -- is precisely the one nothing else could reconstruct. A stated filter
	// selects a subset instead.
	//
	// Either way the union adds the underlying of every derivative matched,
	// whether or not the filter would have. A file names an underlying by
	// identifier and the archive requires that instrument to appear in the same
	// part, so an OPTION exported without its STOCK is not a partial export but
	// an invalid one. An asset-class filter of {OPTION} is the ordinary way to
	// hit that.
	//
	// The canonical-identifier guard stays: an instrument no canonical
	// identifier names cannot be written to a file at all.
	matched := `
		SELECT i.id
		FROM instruments i
		WHERE (EXISTS (SELECT 1 FROM instrument_identifiers ii WHERE ii.instrument_id = i.id AND ii.canonical = true)
		       OR EXISTS (
		            SELECT 1 FROM instrument_listing_identifiers li
		            WHERE li.instrument_id = i.id AND li.canonical = true
		          ))`

	args := []interface{}{}
	argN := 1

	if len(assetClasses) > 0 {
		matched += fmt.Sprintf("\n\t\t\tAND i.asset_class = ANY($%d)", argN)
		args = append(args, pq.Array(assetClasses))
		argN++
	}
	// A security is admitted to no venue, its lines are, so the filter asks
	// whether any line of the security is admitted to this one. Permissive by the
	// same rule the venue set itself is read by: the set is what we have been told
	// about, so a security whose venue nobody ever stated as a MIC_TICKER domain
	// is not selected -- there is no venue recorded for it to be selected on. See
	// docs/adr/0077-a-venue-set-is-what-we-know-not-what-exists.md.
	if exchangeFilter != "" {
		matched += fmt.Sprintf(`
			AND EXISTS (
			     SELECT 1 FROM instrument_listings l
			     JOIN listing_venues v ON v.listing_id = l.id
			     WHERE l.instrument_id = i.id AND v.mic = $%d
			   )`, argN)
		args = append(args, exchangeFilter)
	}

	base := `
		WITH matched AS (` + matched + `
		), selected AS (
			SELECT id FROM matched
			UNION
			SELECT ul.instrument_id FROM instruments d
			JOIN matched m ON m.id = d.id
			JOIN instrument_listings ul ON ul.id = d.underlying_listing_id
		)
		SELECT i.id, i.asset_class, i.name, ` + underlyingSelect + `,
		       i.cik, i.sic_code,
		       i.strike, i.expiry, i.put_call, i.contract_multiplier,
		       u_id.identifier_type AS underlying_identifier_type,
		       u_id.value AS underlying_identifier_value,
		       COALESCE(u_id.domain, '') AS underlying_identifier_domain,
		       u_l.currency AS underlying_currency
		FROM instruments i
		JOIN selected s ON s.id = i.id
		` + underlyingJoin +
		// The listing join, because what a contract delivers is one currency
		// line of the underlying and the file names that line: this identifier
		// and u_l.currency beside it. See
		// docs/adr/0074-an-options-underlying-is-the-line-its-strike-is-quoted-in.md.
		bestListingIdentifierJoinOn("LEFT JOIN", "i.underlying_listing_id", "u_id") + `
		ORDER BY i.id`

	err = p.q.SelectContext(ctx, &irows, base, args...)
	if err != nil {
		return nil, fmt.Errorf("list instruments for export: %w", err)
	}
	results := make([]*db.InstrumentRow, len(irows))
	ids := make([]uuid.UUID, len(irows))
	for i := range irows {
		results[i] = irows[i].toDBRow()
		ids[i] = irows[i].ID
	}
	if err := loadIdentifiers(ctx, p.q, ids, results); err != nil {
		return nil, fmt.Errorf("list identifiers for export: %w", err)
	}
	if err := loadProviderIdentifiers(ctx, p.q, ids, results); err != nil {
		return nil, fmt.Errorf("list provider identifiers for export: %w", err)
	}
	if err := loadListings(ctx, p.q, ids, results); err != nil {
		return nil, fmt.Errorf("list listings for export: %w", err)
	}
	return results, nil
}

// ListInstrumentsByIDs implements db.InstrumentDB.
func (p *Postgres) ListInstrumentsByIDs(ctx context.Context, ids []string) ([]*db.InstrumentRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool)
	var uuids []uuid.UUID
	for _, s := range ids {
		if s == "" || seen[s] {
			continue
		}
		parsed, err := uuid.Parse(s)
		if err != nil {
			continue
		}
		seen[s] = true
		uuids = append(uuids, parsed)
	}
	if len(uuids) == 0 {
		return nil, nil
	}
	inClause, args := inClauseUUIDs(uuids)
	var irows []instrumentRow
	err := p.q.SelectContext(ctx, &irows, fmt.Sprintf(`
		SELECT i.id, i.asset_class, i.name, `+underlyingSelect+`,
		       i.cik, i.sic_code,
		       i.strike, i.expiry, i.put_call, i.contract_multiplier
		FROM instruments i
		`+underlyingJoin+`
		WHERE i.id IN (%s)
	`, inClause), args...)
	if err != nil {
		return nil, fmt.Errorf("list instruments by ids: %w", err)
	}
	results := make([]*db.InstrumentRow, len(irows))
	resultIDs := make([]uuid.UUID, len(irows))
	for i := range irows {
		results[i] = irows[i].toDBRow()
		resultIDs[i] = irows[i].ID
	}
	if err := loadIdentifiers(ctx, p.q, resultIDs, results); err != nil {
		return nil, fmt.Errorf("list identifiers for instruments: %w", err)
	}
	if err := loadProviderIdentifiers(ctx, p.q, resultIDs, results); err != nil {
		return nil, fmt.Errorf("list provider identifiers for instruments: %w", err)
	}
	if err := loadListings(ctx, p.q, resultIDs, results); err != nil {
		return nil, fmt.Errorf("list listings for instruments: %w", err)
	}
	return results, nil
}

// EnsureInstrument implements db.InstrumentDB.
// Finds by any identifier; if not found, creates instrument and inserts identifiers.
// When multiple identifiers resolve to different instruments, merges them eagerly and returns the survivor.
// On unique violation (identifier already exists for another instrument), returns the existing instrument ID (eager merge).
//
// claims is what the caller's answers actually asserted, kept apart by the
// answer that produced them, and it is what decides the merge. A set the caller
// assembled from several results is not an association anybody stated -- two
// results agreeing about a currency and a venue have not said they are the same
// security -- so identifiers landing on two instruments merge them only where
// one claim named both and the stored rows at each end may carry a chain. A
// caller with a single identifier and nothing to assert passes none, and never
// reaches more than one instrument to have to.
func (p *Postgres) EnsureInstrument(ctx context.Context, assetClass, currency, name, cik, sicCode string, identifiers []db.IdentifierInput, claims []db.IdentityClaim, underlyingListingID string, optionFields *db.OptionFields) (string, string, error) {
	return p.ensureSecurity(ctx, assetClass, currency, name, cik, sicCode, identifiers, claims, underlyingListingID, optionFields, nil)
}

// EnsureArchiveInstrument implements db.InstrumentDB.
//
// The security-grain identifiers and every line's are handed to the shared core
// together, so the lookup sees the whole of what the file says the instrument is
// called and a security known by one line's ticker alone is still matched. Where
// each of those names is then stored is the placement, which files them per line.
func (p *Postgres) EnsureArchiveInstrument(ctx context.Context, assetClass, name, cik, sicCode string,
	identifiers []db.IdentifierInput, listings db.ListingSet, claims []db.IdentityClaim,
	underlyingListingID string, optionFields *db.OptionFields) (string, string, error) {
	all := make([]db.IdentifierInput, 0, len(identifiers))
	all = append(all, identifiers...)
	for _, l := range listings.Listings {
		all = append(all, l.Identifiers...)
	}
	all = append(all, listings.Unplaced...)
	// The lines are keyed on currency, and normalising a MIC_TICKER's domain is
	// the core's job, so the placement reads the set the core has normalised
	// rather than the caller's copy of it.
	place := func(ctx context.Context, exec queryable, instrumentID uuid.UUID) (uuid.UUID, error) {
		return p.placeArchiveListings(ctx, exec, instrumentID, listings)
	}
	// No currency: the placement knows every line the file states, so there is no
	// single one for the core to settle.
	return p.ensureSecurity(ctx, assetClass, "", name, cik, sicCode, all, claims,
		underlyingListingID, optionFields, place)
}

// placeArchiveListings ensures every line a file states and files that line's own
// names and provider identifiers on it, then files the names it placed on no line
// against the security.
//
// It returns the first line the file named, which is what the security's own
// currency column is filled from and what a caller with one line in mind gets
// back. No line is primary -- the first is simply the one the file wrote first,
// and a file naming no line returns none.
func (p *Postgres) placeArchiveListings(ctx context.Context, exec queryable, instrumentID uuid.UUID, set db.ListingSet) (uuid.UUID, error) {
	var first uuid.UUID
	for _, l := range set.Listings {
		listingID, err := ensureListing(ctx, exec, instrumentID, l.Currency)
		if err != nil {
			return uuid.Nil, err
		}
		if listingID == uuid.Nil {
			return uuid.Nil, fmt.Errorf("place listing %q: the security has several lines and none of them is this one", l.Currency)
		}
		if first == uuid.Nil {
			first = listingID
		}
		idns := make([]db.IdentifierInput, len(l.Identifiers))
		copy(idns, l.Identifiers)
		for i := range idns {
			if idns[i].Ref.Type == "MIC_TICKER" && idns[i].Ref.Domain != "" {
				idns[i].Ref.Domain = p.normalizeToOperatingMIC(ctx, idns[i].Ref.Domain)
			}
		}
		if err := insertListingIdentifiers(ctx, exec, instrumentID, &listingID, idns); err != nil {
			return uuid.Nil, err
		}
		// The interval the line was tradeable in, which nothing recomputes. A
		// stored value wins, as it does for every column the merge fills.
		if l.ValidFrom != nil || l.ValidBefore != nil {
			if _, err := exec.ExecContext(ctx, `
				UPDATE instrument_listings
				SET valid_from = COALESCE(valid_from, $2), valid_before = COALESCE(valid_before, $3)
				WHERE id = $1 AND (valid_from IS NULL OR valid_before IS NULL)
			`, listingID, nullTime(l.ValidFrom), nullTime(l.ValidBefore)); err != nil {
				return uuid.Nil, fmt.Errorf("place listing interval (%s): %w", l.Currency, err)
			}
		}
		if err := insertListingProviderIdentifiers(ctx, exec, instrumentID, &listingID, l.ProviderIdentifiers); err != nil {
			return uuid.Nil, err
		}
	}
	idns := make([]db.IdentifierInput, len(set.Unplaced))
	copy(idns, set.Unplaced)
	for i := range idns {
		if idns[i].Ref.Type == "MIC_TICKER" && idns[i].Ref.Domain != "" {
			idns[i].Ref.Domain = p.normalizeToOperatingMIC(ctx, idns[i].Ref.Domain)
		}
	}
	if err := insertListingIdentifiers(ctx, exec, instrumentID, nil, idns); err != nil {
		return uuid.Nil, err
	}
	if err := insertListingProviderIdentifiers(ctx, exec, instrumentID, nil, set.UnplacedProvider); err != nil {
		return uuid.Nil, err
	}
	return first, nil
}

// ensureSecurity is what EnsureInstrument and EnsureArchiveInstrument share: find
// the security these identifiers name, merge where they name several, create one
// where they name none, and fill in what it does not already have.
//
// place is where the two part company. Nil means the caller speaks of a single
// currency and its listing-grain names go on the line that currency names, which
// is EnsureInstrument's whole listing rule; an archive hands in a placement that
// knows the security's lines one by one.
func (p *Postgres) ensureSecurity(ctx context.Context, assetClass, currency, name, cik, sicCode string, identifiers []db.IdentifierInput, claims []db.IdentityClaim, underlyingListingID string, optionFields *db.OptionFields, place placeListings) (string, string, error) {
	if len(identifiers) == 0 {
		return "", "", fmt.Errorf("at least one identifier required")
	}
	if assetClass != "" && !db.ValidAssetClass(assetClass) {
		return "", "", fmt.Errorf("invalid asset_class %q", assetClass)
	}
	// A derivative that could not be told which line it delivers is not a
	// derivative this can store: without a currency there is no way to read the
	// strike. Callers degrade to a broker-description-only instrument rather than
	// guessing, as they already did when the underlying itself was unresolvable.
	// See docs/adr/0074-an-options-underlying-is-the-line-its-strike-is-quoted-in.md.
	if (assetClass == db.AssetClassOption || assetClass == db.AssetClassFuture) && underlyingListingID == "" {
		return "", "", fmt.Errorf("underlying_listing_id required when asset_class is %s", assetClass)
	}
	// Normalize MIC_TICKER domains to operating MICs. It is the identifier's
	// domain and nothing else: a venue is recorded against a line, and a line
	// records one by holding the ticker that names it there.
	for i := range identifiers {
		if identifiers[i].Ref.Type == "MIC_TICKER" && identifiers[i].Ref.Domain != "" {
			identifiers[i].Ref.Domain = p.normalizeToOperatingMIC(ctx, identifiers[i].Ref.Domain)
		}
	}
	var underlyingUUID *uuid.UUID
	if underlyingListingID != "" {
		parsed, err := uuid.Parse(underlyingListingID)
		if err != nil {
			return "", "", fmt.Errorf("invalid underlying_listing_id: %w", err)
		}
		if err := requireListing(ctx, p.q, parsed); err != nil {
			return "", "", err
		}
		underlyingUUID = &parsed
	}
	// Each identifier is stored against what its type names. The split is taken
	// once here and carried through every branch below, so no branch can decide
	// it differently.
	securityIDs, listingIDs := splitByGrain(identifiers)
	// Where this entry point's listing-grain names go. See placeListings.
	//
	// placeWrites says whether it has anything to write, which is what lets the
	// hot path below read a line rather than open a transaction to settle one. A
	// caller supplying its own placement always has something to say.
	placeWrites := place != nil || len(listingIDs) > 0
	if place == nil {
		place = oneLine(currency, listingIDs)
	}
	// Look up every identifier and collect distinct instrument IDs (no early
	// return). What the row holding each one says is kept alongside: merge
	// admission asks about the stored association rather than about the value,
	// so the interval the row was written with is part of the question.
	seen := make(map[uuid.UUID]struct{})
	var distinctIDs []uuid.UUID
	held := make(map[string]endpoint, len(identifiers))
	for _, idn := range identifiers {
		// findHolder asks the table the type names, so a mixed set is looked up
		// a row at a time at each row's own grain.
		e, ok, err := findHolder(ctx, p.q, idn.Ref)
		if err != nil {
			return "", "", fmt.Errorf("lookup instrument: %w", err)
		}
		if !ok {
			continue
		}
		held[refKey(idn.Ref)] = e
		if _, dup := seen[e.instrument]; !dup {
			seen[e.instrument] = struct{}{}
			distinctIDs = append(distinctIDs, e.instrument)
		}
	}
	// The instruments a claim corroborated as one, anchored on the first entry in
	// distinctIDs -- which is in caller order, so it is the instrument the
	// caller's highest-precedence identifier reached: the winner's own answer,
	// where the caller is a resolution, since the broker description it appends
	// is last.
	//
	// Asked wherever the lookup found anything rather than only where it found
	// two, because a claim reaches beyond the identifiers the caller supplied. A
	// value a result was strictly filtered on is corroborated rather than
	// returned, so it is never written and never in the caller's set, and it
	// names the association as loudly as a returned value does (adr/0060).
	var group []uuid.UUID
	if len(distinctIDs) > 0 {
		var err error
		group, err = p.mergeGroup(ctx, distinctIDs[0], claims, held)
		if err != nil {
			return "", "", err
		}
	}
	// Merge what the group holds and return the survivor. A group of one is the
	// refusal: nothing corroborated the instruments this landed on as one, so the
	// merge loop does nothing, the anchor is what the transaction attaches to,
	// and every other instrument is left exactly as it was.
	//
	// A refusal is not recorded here. This layer has no run to record it against
	// and no logger to say it out loud, and a durable record attached to the
	// security is 0141, which reads the same two endpoints this does.
	//
	// The refused case deliberately does not complete the anchor in place the way
	// the single-instrument branch below does. Another instrument holds the
	// identifiers over an overlapping interval, which is what the exclusion
	// constraint on instrument_identifiers says cannot be written twice.
	if len(group) > 1 || len(distinctIDs) > 1 {
		survivor, err := pickSurvivor(ctx, p.q, group)
		if err != nil {
			return "", "", err
		}
		var listingID uuid.UUID
		err = p.runInTx(ctx, func(exec queryable) error {
			for _, id := range group {
				if id == survivor {
					continue
				}
				if err := mergeInstruments(ctx, exec, survivor, id); err != nil {
					return err
				}
			}
			// The survivor's lines, and the caller's listing-grain identifiers
			// filed on them. Minting a line that is not there yet is the same
			// act the create path performs, and it is idempotent for a survivor
			// that already has one.
			listingID, err = place(ctx, exec, survivor)
			if err != nil {
				return err
			}
			// Update underlying and option fields on the survivor.
			return updateInstrumentOnMatch(ctx, exec, survivor, underlyingUUID, optionFields)
		})
		if err != nil {
			return "", "", err
		}
		return survivor.String(), nilUUIDToString(listingID), nil
	}
	// Exactly one instrument: update underlying and option fields, and complete
	// it outright where it had no identity to begin with.
	if len(distinctIDs) == 1 {
		id := distinctIDs[0]
		descOnly, err := holdsNoCanonicalIdentifier(ctx, p.q, id)
		if err != nil {
			return "", "", err
		}
		if !descOnly {
			// The line the currency names, read rather than written where the
			// security already has it and there is nothing to file on it. This
			// is the path a bulk import takes on almost every row, so it stays
			// one statement outside a transaction; anything else goes through
			// placement, which writes.
			listingID, err := listingFor(ctx, p.q, id, currency)
			if err != nil {
				return "", "", err
			}
			if listingID == uuid.Nil || placeWrites {
				err = p.runInTx(ctx, func(exec queryable) error {
					var lErr error
					listingID, lErr = place(ctx, exec, id)
					return lErr
				})
				if err != nil {
					return "", "", err
				}
			}
			if err := updateInstrumentOnMatch(ctx, p.q, id, underlyingUUID, optionFields); err != nil {
				return "", "", err
			}
			return id.String(), nilUUIDToString(listingID), nil
		}
		// The instrument is nothing but a broker's text for a security: it holds
		// no canonical identifier and every column is null. So what identified it
		// is written on, rather than found and dropped as it is for an instrument
		// that already has an identity.
		//
		// This asserts no association between two identities and chains nothing
		// through the description (adr/0061), because there is no second identity
		// here -- the identifiers arrive together and become this instrument's
		// first. It is also the one write adr/0004's "a stored value wins" has
		// nothing to protect: there is no stored value.
		//
		// An instrument that does hold a canonical identifier is left alone. What
		// may be added to one is 0136, and it is a different question with a
		// different answer.
		var listingID uuid.UUID
		err = p.runInTx(ctx, func(exec queryable) error {
			if mErr := mergeIntoInstrument(ctx, exec, id, db.InstrumentMerge{
				AssetClass:  assetClass,
				CIK:         cik,
				SICCode:     sicCode,
				Identifiers: securityIDs,
			}); mErr != nil {
				return mErr
			}
			var lErr error
			listingID, lErr = place(ctx, exec, id)
			if lErr != nil {
				return lErr
			}
			return updateInstrumentOnMatch(ctx, exec, id, underlyingUUID, optionFields)
		})
		if err != nil {
			return "", "", err
		}
		return id.String(), nilUUIDToString(listingID), nil
	}
	// None found: create new instrument and add identifiers.
	var newID, newListingID uuid.UUID
	err := p.runInTx(ctx, func(exec queryable) error {
		var strike, expiry, putCall any
		if optionFields != nil {
			strike = optionFields.Strike
			expiry = optionFields.Expiry
			putCall = optionFields.PutCall
		}
		err := exec.QueryRowContext(ctx, `
			INSERT INTO instruments (asset_class, name, cik, sic_code, underlying_listing_id, strike, expiry, put_call)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id
		`, nullStr(assetClass), nullStr(name), nullStr(cik), nullStr(sicCode), nullUUID(underlyingUUID), strike, expiry, putCall).Scan(&newID)
		if err != nil {
			return err
		}
		for _, idn := range securityIDs {
			_, err = exec.ExecContext(ctx, `INSERT INTO instrument_identifiers (instrument_id, identifier_type, domain, value, canonical, valid_from, valid_before) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				newID, idn.Ref.Type, nullStr(idn.Ref.Domain), idn.Ref.Value, idn.Canonical, nullTime(idn.ValidFrom), nullTime(idn.ValidBefore))
			if err != nil {
				if isIdentifierConflict(err) {
					return errIdentifierExists // rollback tx; caller will look up existing id
				}
				return err
			}
		}
		// Every security has at least one currency line, and a security created
		// without a stated currency gets the unknown one rather than none: how
		// many lines it has is what is unknown, not whether it has any. Placement
		// mints the lines and files the listing-grain names on them.
		newListingID, err = place(ctx, exec, newID)
		return err
	})
	if err != nil {
		if errors.Is(err, errIdentifierExists) {
			// The losing race looks the winner up at each identifier's own
			// grain, which is what FindInstrumentByIdentifier dispatches on.
			for _, idn := range identifiers {
				existingID, rowErr := p.FindInstrumentByIdentifier(ctx, idn.Ref.Type, idn.Ref.Domain, idn.Ref.Value)
				if rowErr == nil && existingID != "" {
					existingUUID, parseErr := uuid.Parse(existingID)
					if parseErr != nil {
						return "", "", parseErr
					}
					var listingID uuid.UUID
					lErr := p.runInTx(ctx, func(exec queryable) error {
						var e error
						listingID, e = ensureListing(ctx, exec, existingUUID, currency)
						return e
					})
					if lErr != nil {
						return "", "", lErr
					}
					return existingID, nilUUIDToString(listingID), nil
				}
			}
		}
		return "", "", err
	}
	return newID.String(), nilUUIDToString(newListingID), nil
}

// nilUUIDToString renders a listing id for the API boundary, where the absence
// of one is "" rather than a nil UUID. Absence is a real answer here: a caller
// that stated no currency for a security with several lines has named none of
// them.
func nilUUIDToString(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

// holdsNoCanonicalIdentifier reports whether an instrument is nothing but a
// broker description. canonical = false marks one of those and nothing else, so
// the absence of a canonical row is the stored form of broker-description-only.
//
// Both grains are asked, because a canonical name at either is an identity. A
// ticker-only instrument answering true here would be completed in place by the
// next resolution that matched its description, which is a write adr/0004
// reserves for an instrument that has no identity at all.
//
// It is the negation of [db.Identified], asked in SQL because this runs where no
// row has been loaded -- the resolution path holds a UUID and nothing else. The
// two are held in lockstep by TestIdentifiedMatchesTheStore rather than by one
// calling the other, in the pattern currency.Family and the SQL currency_family
// follow.
func holdsNoCanonicalIdentifier(ctx context.Context, exec queryable, id uuid.UUID) (bool, error) {
	var exists bool
	err := exec.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM instrument_identifiers WHERE instrument_id = $1 AND canonical)
		    OR EXISTS (
		         SELECT 1 FROM instrument_listing_identifiers li
		         WHERE li.instrument_id = $1 AND li.canonical
		       )
	`, id).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check canonical identifiers: %w", err)
	}
	return !exists, nil
}

// updateInstrumentOnMatch optionally sets underlying_listing_id and option
// fields on an existing instrument. It writes no identifier, which is what leaves each name's
// valid_from where it was: matching an existing instrument is not evidence that
// any of its names became correct today, and moving them is what used to disarm
// the retroactive option-split guard.
//
// An instrument holding no identity at all is the exception, handled by its
// caller: an absent name is inserted with the vintage the resolution stamped on
// it (adr/0055), which is a different act from moving one that is already there.
func updateInstrumentOnMatch(ctx context.Context, exec queryable, id uuid.UUID, underlyingListingID *uuid.UUID, optionFields *db.OptionFields) error {
	if optionFields != nil {
		_, err := exec.ExecContext(ctx, `
			UPDATE instruments SET underlying_listing_id = COALESCE($2, underlying_listing_id), strike = $3, expiry = $4, put_call = $5
			WHERE id = $1
		`, id, nullUUID(underlyingListingID), optionFields.Strike, optionFields.Expiry, optionFields.PutCall)
		return err
	}
	_, err := exec.ExecContext(ctx, `UPDATE instruments SET underlying_listing_id = COALESCE($2, underlying_listing_id) WHERE id = $1`, id, nullUUID(underlyingListingID))
	return err
}

// SetContractMultiplier implements db.InstrumentDB.
func (p *Postgres) SetContractMultiplier(ctx context.Context, instrumentID string, m decimal.Decimal) error {
	id, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("invalid instrument id: %w", err)
	}
	if _, err := p.q.ExecContext(ctx, `UPDATE instruments SET contract_multiplier = $2 WHERE id = $1`, id, m); err != nil {
		return fmt.Errorf("set contract multiplier: %w", err)
	}
	return nil
}

// ListInstruments implements db.InstrumentDB.
func (p *Postgres) ListInstruments(ctx context.Context, search string, assetClasses []string, pageSize int32, pageToken string) ([]*db.InstrumentRow, int32, string, error) {
	limit := pageSize
	offset := decodePageToken(pageToken)

	// Build shared WHERE conditions.
	where := sq.And{}
	if search != "" {
		where = append(where, sq.ILike{"i.name": "%" + search + "%"})
	}
	if len(assetClasses) > 0 {
		var filtered []string
		includeEmpty := false
		for _, ac := range assetClasses {
			if ac == "UNKNOWN" {
				includeEmpty = true
			} else {
				filtered = append(filtered, ac)
			}
		}
		var parts sq.Or
		if len(filtered) > 0 {
			parts = append(parts, sq.Eq{"i.asset_class": filtered})
		}
		if includeEmpty {
			parts = append(parts, sq.Or{sq.Eq{"i.asset_class": nil}, sq.Eq{"i.asset_class": ""}})
		}
		where = append(where, parts)
	}

	// Count total matching instruments.
	countQ, countArgs, err := psql.Select("COUNT(*)").From("instruments i").Where(where).ToSql()
	if err != nil {
		return nil, 0, "", fmt.Errorf("build count instruments query: %w", err)
	}
	var total int32
	if err := p.q.QueryRowContext(ctx, countQ, countArgs...).Scan(&total); err != nil {
		return nil, 0, "", fmt.Errorf("count instruments: %w", err)
	}
	if total == 0 {
		return nil, 0, "", nil
	}

	q, args, err := psql.Select(
		"i.id", "i.asset_class", "i.name", underlyingSelect,
		"i.cik", "i.sic_code",
		"i.strike", "i.expiry", "i.put_call", "i.contract_multiplier",
	).
		From("instruments i").
		LeftJoin("instrument_listings u_l ON u_l.id = i.underlying_listing_id").
		Where(where).
		OrderBy("lower(i.name)").
		Limit(uint64(limit + 1)).Offset(uint64(offset)).
		ToSql()
	if err != nil {
		return nil, 0, "", fmt.Errorf("build list instruments query: %w", err)
	}

	var irows []instrumentRow
	if err := p.q.SelectContext(ctx, &irows, q, args...); err != nil {
		return nil, 0, "", fmt.Errorf("list instruments: %w", err)
	}

	// Compute next page token (we fetched limit+1 to detect more pages).
	var nextToken string
	if int32(len(irows)) > limit {
		irows = irows[:limit]
		nextToken = encodePageToken(offset + int64(limit))
	}

	results := make([]*db.InstrumentRow, len(irows))
	ids := make([]uuid.UUID, len(irows))
	for i := range irows {
		results[i] = irows[i].toDBRow()
		ids[i] = irows[i].ID
	}
	if err := loadIdentifiers(ctx, p.q, ids, results); err != nil {
		return nil, 0, "", fmt.Errorf("list instrument identifiers: %w", err)
	}
	if err := loadProviderIdentifiers(ctx, p.q, ids, results); err != nil {
		return nil, 0, "", fmt.Errorf("list instrument provider identifiers: %w", err)
	}
	if err := loadListings(ctx, p.q, ids, results); err != nil {
		return nil, 0, "", fmt.Errorf("list instrument listings: %w", err)
	}
	return results, total, nextToken, nil
}

// ValidateMIC implements db.InstrumentDB.
func (p *Postgres) ValidateMIC(ctx context.Context, mic string) (bool, error) {
	var n int
	err := p.q.QueryRowContext(ctx, `SELECT 1 FROM exchanges WHERE mic = $1`, mic).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("validate mic: %w", err)
	}
	return true, nil
}

// InsertInstrumentIdentifier implements db.InstrumentDB.
//
// A listing-grain row is filed on the line the caller names, and on none where it
// names none: a security with two lines has no way to work out which one a bare
// ticker meant, and the row says so rather than picking one.
func (p *Postgres) InsertInstrumentIdentifier(ctx context.Context, instrumentID, listingID string, input db.IdentifierInput) error {
	if input.Ref.Type == "MIC_TICKER" && input.Ref.Domain != "" {
		input.Ref.Domain = p.normalizeToOperatingMIC(ctx, input.Ref.Domain)
	}
	uid, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("insert instrument identifier: invalid id: %w", err)
	}
	if identifier.NamesAListing(input.Ref.Type) {
		lid, err := optionalUUID(listingID)
		if err != nil {
			return fmt.Errorf("insert listing identifier %s: %w", input.Ref.Type, err)
		}
		// No ON CONFLICT here, unlike the placement path: this caller is asking
		// for the row to exist, so a name another line already holds is an answer
		// it needs rather than a gap to leave unfilled.
		_, err = p.q.ExecContext(ctx, `
			INSERT INTO instrument_listing_identifiers (instrument_id, listing_id, identifier_type, domain, value, canonical, valid_from, valid_before)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, uid, lid, input.Ref.Type, nullStr(input.Ref.Domain), input.Ref.Value, input.Canonical, nullTime(input.ValidFrom), nullTime(input.ValidBefore))
		if err != nil {
			return fmt.Errorf("insert listing identifier: %w", err)
		}
		return nil
	}
	_, err = p.q.ExecContext(ctx, `
		INSERT INTO instrument_identifiers (instrument_id, identifier_type, domain, value, canonical, valid_from, valid_before)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, uid, input.Ref.Type, nullStr(input.Ref.Domain), input.Ref.Value, input.Canonical, nullTime(input.ValidFrom), nullTime(input.ValidBefore))
	if err != nil {
		return fmt.Errorf("insert instrument identifier: %w", err)
	}
	return nil
}

// MergeInstrumentFromArchive implements db.InstrumentDB.
//
// A file importing onto an instrument that already exists must not rewrite it:
// the target's own reference data is at least as good as the file's, and the
// seeded currency and FX rows are the collision every rebuild hits. So this
// fills gaps only -- identifiers the row lacks, and columns still NULL.
//
// ON CONFLICT DO NOTHING is right rather than lax. EnsureInstrument has already
// looked up every identifier the file states and eagerly merged when two of them
// named different instruments, so a conflict surviving to here names this same
// row.
//
// name is deliberately not merged: recompute_instrument_name derives it from the
// identifiers, and the archive treats it as advisory for that reason.
func (p *Postgres) MergeInstrumentFromArchive(ctx context.Context, instrumentID string, in db.InstrumentMerge) error {
	uid, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("merge instrument: invalid id: %w", err)
	}
	idns := make([]db.IdentifierInput, len(in.Identifiers))
	copy(idns, in.Identifiers)
	for i := range idns {
		if idns[i].Ref.Type == "MIC_TICKER" && idns[i].Ref.Domain != "" {
			idns[i].Ref.Domain = p.normalizeToOperatingMIC(ctx, idns[i].Ref.Domain)
		}
	}
	in.Identifiers = idns
	return p.runInTx(ctx, func(exec queryable) error {
		if err := mergeIntoInstrument(ctx, exec, uid, in); err != nil {
			return err
		}
		// No currency, so this places the file's listing-grain names and mints
		// nothing. EnsureArchiveInstrument has already ensured every line the
		// file states, and a line this could mint is one the file did not.
		_, err := oneLine("", listingGrain(in.Identifiers))(ctx, exec, uid)
		return err
	})
}

// listingGrain is the listing-grain half of an identifier set, for a caller that
// wants only that half.
func listingGrain(idns []db.IdentifierInput) []db.IdentifierInput {
	_, listing := splitByGrain(idns)
	return listing
}

// mergeIntoInstrument adds what an instrument does not already have: the
// security-grain identifiers it lacks, and columns that are still NULL. A stored
// value always wins (adr/0004), so it fills blanks and never rewrites.
//
// It does no listing work. Which lines a security has, and which of them each
// listing-grain name belongs on, is what the two entry points disagree about, so
// it is a placeListings run by the caller rather than a rule buried here.
//
// It takes an exec rather than opening its own transaction because both callers
// already have one to run in, and because the second of them -- EnsureInstrument
// completing a broker-description-only instrument -- has other writes that must
// land or fail with these.
//
// MIC normalisation is the caller's: EnsureInstrument has already done it for the
// whole identifier set, and doing it twice would ask the exchange table the same
// question again.
//
// name is not among the columns. A trigger derives it from the identifiers in
// force, so an inserted MIC_TICKER takes over from the broker description on its
// own, and writing it here would fight that.
func mergeIntoInstrument(ctx context.Context, exec queryable, id uuid.UUID, in db.InstrumentMerge) error {
	securityIDs, _ := splitByGrain(in.Identifiers)
	for _, idn := range securityIDs {
		// ON CONFLICT with no target covers the exclusion constraint as well as
		// a unique index, so an identifier the row already holds over an
		// overlapping interval is still a no-op rather than an error.
		_, err := exec.ExecContext(ctx, `
			INSERT INTO instrument_identifiers (instrument_id, identifier_type, domain, value, canonical, valid_from, valid_before)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT DO NOTHING
		`, id, idn.Ref.Type, nullStr(idn.Ref.Domain), idn.Ref.Value, idn.Canonical, nullTime(idn.ValidFrom), nullTime(idn.ValidBefore))
		if err != nil {
			return fmt.Errorf("merge identifier (%s/%s): %w", idn.Ref.Type, idn.Ref.Value, err)
		}
	}
	// The WHERE guard leaves a row that needs nothing unwritten, so a file whose
	// instruments are all already complete does no writes at all.
	_, err := exec.ExecContext(ctx, `
		UPDATE instruments SET
			asset_class = COALESCE(asset_class, $2),
			cik = COALESCE(cik, $3),
			sic_code = COALESCE(sic_code, $4)
		WHERE id = $1
		  AND (asset_class IS NULL OR cik IS NULL OR sic_code IS NULL)
	`, id, nullStr(in.AssetClass), nullStr(in.CIK), nullStr(in.SICCode))
	if err != nil {
		return fmt.Errorf("merge instrument columns: %w", err)
	}
	return nil
}

// placeListings settles a security's currency lines and files the listing-grain
// names on them, returning the line a caller asking about one currency should
// get back. It is the one thing the two entry points disagree about: a caller
// speaking of one currency has one line to fill, and a file carrying a security's
// whole listing set has one per line.
//
// It runs inside its caller's transaction, because a line minted for names that
// then fail to land is a line nobody asked for.
type placeListings func(ctx context.Context, exec queryable, instrumentID uuid.UUID) (uuid.UUID, error)

// oneLine is placement for a caller that speaks of a single currency: the line
// that currency names, with every listing-grain name it supplied filed there.
//
// A caller that stated no currency has named no line, and its names are filed
// against the security with none. That is not a failure to place them -- a ticker
// nobody could pair with a currency names a line of this security and nothing
// says which, which is exactly what the row then records. It is claimed when the
// security comes to have one line for it to mean. See
// docs/adr/0075-a-name-that-could-not-be-placed-names-no-line.md.
func oneLine(currency string, listingIDs []db.IdentifierInput) placeListings {
	return func(ctx context.Context, exec queryable, instrumentID uuid.UUID) (uuid.UUID, error) {
		listingID, err := ensureListing(ctx, exec, instrumentID, currency)
		if err != nil {
			return uuid.Nil, err
		}
		if len(listingIDs) == 0 {
			return listingID, nil
		}
		var on *uuid.UUID
		if listingID != uuid.Nil {
			on = &listingID
		}
		return listingID, insertListingIdentifiers(ctx, exec, instrumentID, on, listingIDs)
	}
}

// insertListingProviderIdentifiers files a line's provider-scoped names on it.
//
// The whole set, unfiltered by grain: the file says these belong to this line, so
// there is nothing left to decide. SaveProviderIdentifiers routes by grain because
// its callers hand it a flat set with no line stated.
func insertListingProviderIdentifiers(ctx context.Context, exec queryable, instrumentID uuid.UUID, listingID *uuid.UUID, pis []db.ProviderIdentifierInput) error {
	for _, pi := range pis {
		_, err := exec.ExecContext(ctx, `
			INSERT INTO provider_listing_identifiers (instrument_id, listing_id, provider, identifier_type, domain, value)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT DO NOTHING
		`, instrumentID, listingID, pi.Provider, pi.Type, nullStr(pi.Domain), pi.Value)
		if err != nil {
			return fmt.Errorf("place listing provider identifier (%s/%s): %w", pi.Provider, pi.Type, err)
		}
	}
	return nil
}

// insertListingIdentifiers files listing-grain names on one line, or on none
// where listingID is nil -- a name nobody could place still names the security.
//
// ON CONFLICT DO NOTHING covers the exclusion constraint as well as a unique
// index, so a name the line already holds over an overlapping interval is a
// no-op. A name another line of the security holds is a disagreement about which
// line it denotes, and the stored answer wins (adr/0004). That covers the
// unplaced case too: a name already filed on a line is not filed a second time
// with the line dropped.
func insertListingIdentifiers(ctx context.Context, exec queryable, instrumentID uuid.UUID, listingID *uuid.UUID, idns []db.IdentifierInput) error {
	for _, idn := range idns {
		_, err := exec.ExecContext(ctx, `
			INSERT INTO instrument_listing_identifiers (instrument_id, listing_id, identifier_type, domain, value, canonical, valid_from, valid_before)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT DO NOTHING
		`, instrumentID, listingID, idn.Ref.Type, nullStr(idn.Ref.Domain), idn.Ref.Value, idn.Canonical, nullTime(idn.ValidFrom), nullTime(idn.ValidBefore))
		if err != nil {
			return fmt.Errorf("place listing identifier (%s/%s): %w", idn.Ref.Type, idn.Ref.Value, err)
		}
	}
	return nil
}

// optionalUUID parses an id a caller may not have: "" is no id rather than an
// invalid one, which is what a caller naming no line has.
func optionalUUID(id string) (*uuid.UUID, error) {
	if id == "" {
		return nil, nil
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("invalid id %q: %w", id, err)
	}
	return &parsed, nil
}

// UpdateInstrumentStrike implements db.InstrumentDB.
func (p *Postgres) UpdateInstrumentStrike(ctx context.Context, instrumentID string, strike decimal.Decimal) error {
	uid, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("update instrument strike: invalid id: %w", err)
	}
	_, err = p.q.ExecContext(ctx, `UPDATE instruments SET strike = $2 WHERE id = $1`, uid, strike)
	if err != nil {
		return fmt.Errorf("update instrument strike: %w", err)
	}
	return nil
}

// SaveProviderIdentifiers implements db.InstrumentDB.
//
// Routed by grain, exactly as the canonical identifiers are. A caller that named
// no line files its listing-grain rows against the security alone: they name a
// line of it, and nothing said which.
func (p *Postgres) SaveProviderIdentifiers(ctx context.Context, instrumentID, listingID string, ids []db.ProviderIdentifierInput) error {
	if len(ids) == 0 {
		return nil
	}
	uid, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("save provider identifiers: invalid id: %w", err)
	}
	securityPIs, listingPIs := splitProviderByGrain(ids)
	if len(securityPIs) > 0 {
		for _, id := range securityPIs {
			_, err := p.q.ExecContext(ctx, `
				INSERT INTO provider_instrument_identifiers (instrument_id, provider, identifier_type, domain, value)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT DO NOTHING
			`, uid, id.Provider, id.Type, nullStr(id.Domain), id.Value)
			if err != nil {
				return fmt.Errorf("save provider identifier (%s/%s): %w", id.Provider, id.Type, err)
			}
		}
	}
	if len(listingPIs) == 0 {
		return nil
	}
	lid, err := optionalUUID(listingID)
	if err != nil {
		return fmt.Errorf("save provider identifiers: %w", err)
	}
	return insertListingProviderIdentifiers(ctx, p.q, uid, lid, listingPIs)
}

// FindProviderIdentifiers implements db.InstrumentDB.
//
// Both grains, because the caller is asking what it may key a request on and
// that is a question about the security and every line of it. This is a
// flattening at the boundary in the sense InstrumentRow.AllIdentifiers is, not a
// lookup: nothing here is searching two tables for one row.
func (p *Postgres) FindProviderIdentifiers(ctx context.Context, instrumentID, provider string) ([]db.ProviderIdentifierInput, error) {
	uid, err := uuid.Parse(instrumentID)
	if err != nil {
		return nil, fmt.Errorf("find provider identifiers: invalid id: %w", err)
	}
	rows, err := p.q.QueryContext(ctx, `
		SELECT provider, identifier_type, domain, value
		FROM provider_instrument_identifiers
		WHERE instrument_id = $1 AND provider = $2
		UNION ALL
		SELECT pli.provider, pli.identifier_type, pli.domain, pli.value
		FROM provider_listing_identifiers pli
		WHERE pli.instrument_id = $1 AND pli.provider = $2
		ORDER BY identifier_type, value
	`, uid, provider)
	if err != nil {
		return nil, fmt.Errorf("find provider identifiers: %w", err)
	}
	defer rows.Close()
	var result []db.ProviderIdentifierInput
	for rows.Next() {
		var pi db.ProviderIdentifierInput
		var domain sql.NullString
		if err := rows.Scan(&pi.Provider, &pi.Type, &domain, &pi.Value); err != nil {
			return nil, err
		}
		if domain.Valid {
			pi.Domain = domain.String
		}
		result = append(result, pi)
	}
	return result, rows.Err()
}

// normalizeToOperatingMIC maps a MIC to its operating MIC. If the lookup fails
// (unknown MIC or DB error), returns the original value unchanged.
func (p *Postgres) normalizeToOperatingMIC(ctx context.Context, mic string) string {
	if mic == "" {
		return mic
	}
	opMIC, err := p.LookupOperatingMIC(ctx, mic)
	if err != nil {
		return mic
	}
	return opMIC
}

// LookupMICCountry implements db.InstrumentDB.
//
// The country is what makes a provider's market-level answer comparable to
// another provider's venue: a composite exchange code names a country's venues
// as a group, and ISO 10383 already records which country each MIC is in.
func (p *Postgres) LookupMICCountry(ctx context.Context, mic string) (string, error) {
	var code string
	err := p.q.QueryRowContext(ctx, `SELECT country_code FROM exchanges WHERE mic = $1`, mic).Scan(&code)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup mic country: %w", err)
	}
	return code, nil
}

// LookupOperatingMIC implements db.InstrumentDB.
func (p *Postgres) LookupOperatingMIC(ctx context.Context, mic string) (string, error) {
	var opMIC string
	err := p.q.QueryRowContext(ctx, `SELECT operating_mic FROM exchanges WHERE mic = $1`, mic).Scan(&opMIC)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("lookup operating mic: unknown MIC %q", mic)
	}
	if err != nil {
		return "", fmt.Errorf("lookup operating mic: %w", err)
	}
	return opMIC, nil
}
