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
// mergedAway's listing rows cascade away with it, so the names they carry are
// moved across first, onto the survivor's line of the same currency family. That
// much of adr/0071's union is required here rather than deferred: a MIC_TICKER
// now lives on a listing, and without the move every eager merge would silently
// drop the ticker it moves today. What an unknown listing does when the survivor
// has known lines to split across is the rest of adr/0071, and is not decided
// here.
func mergeInstruments(ctx context.Context, exec queryable, survivor, mergedAway uuid.UUID) error {
	if survivor == mergedAway {
		return nil
	}
	// weight_commodity moves with instrument_id, in the same statement: a posting
	// weighing in its own security names it by instrument, so leaving the name behind
	// would split one commodity into two and unbalance the group. Only the 'inst:'
	// form needs rewriting -- a converted or cash leg is named by its currency code,
	// which the merge does not change. Both legs of a same-instrument group move
	// together, so the group stays balanced across the merge. See
	// docs/adr/0029-posting-weight-is-stored.md.
	if _, err := exec.ExecContext(ctx, `
		UPDATE txs
		SET instrument_id = $1::uuid,
		    weight_commodity = CASE WHEN weight_commodity = 'inst:' || $2::uuid::text
		                            THEN 'inst:' || $1::uuid::text
		                            ELSE weight_commodity END
		WHERE instrument_id = $2::uuid
	`, survivor, mergedAway); err != nil {
		return fmt.Errorf("update txs: %w", err)
	}
	// A transfer match is keyed on the commodity in flight, so it moves with the
	// postings it links. Left behind it would point at an instrument the delete
	// below is about to remove, and the pair would look unmatched again.
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
		UPDATE transfer_matches SET instrument_id = $1::uuid WHERE instrument_id = $2::uuid
	`, survivor, mergedAway); err != nil {
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
	if err := mergeListingIdentifiers(ctx, exec, survivor, mergedAway); err != nil {
		return err
	}
	// Update any instruments that referenced mergedAway as their underlying.
	if _, err := exec.ExecContext(ctx, `UPDATE instruments SET underlying_id = $1 WHERE underlying_id = $2`, survivor, mergedAway); err != nil {
		return fmt.Errorf("update instruments.underlying_id: %w", err)
	}
	if _, err := exec.ExecContext(ctx, `DELETE FROM instruments WHERE id = $1`, mergedAway); err != nil {
		return fmt.Errorf("delete merged instrument: %w", err)
	}
	return nil
}

// mergeListingIdentifiers moves the names on each of mergedAway's listings onto
// the survivor's listing of the same currency family, minting one where the
// survivor has no line in that family yet.
//
// The family and not the code, so a line stored in GBX and one stored in GBP are
// one line and their names end up together -- which is the whole reason listing
// uniqueness is on the family.
//
// Delete-then-insert rather than an UPDATE of listing_id, for the reason the
// security-grain move above has it: the overlap constraint is global, so a name
// the survivor already holds over the same interval would fail the merge, and
// skipping it is right -- the survivor holding it already is the two rows saying
// the same thing.
func mergeListingIdentifiers(ctx context.Context, exec queryable, survivor, mergedAway uuid.UUID) error {
	rows, err := exec.QueryContext(ctx, `
		SELECT id, COALESCE(currency, '') FROM instrument_listings WHERE instrument_id = $1
	`, mergedAway)
	if err != nil {
		return fmt.Errorf("list merged listings: %w", err)
	}
	defer rows.Close()
	type listing struct {
		id       uuid.UUID
		currency string
	}
	var listings []listing
	for rows.Next() {
		var l listing
		if err := rows.Scan(&l.id, &l.currency); err != nil {
			return fmt.Errorf("scan merged listing: %w", err)
		}
		listings = append(listings, l)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, l := range listings {
		target, err := ensureListing(ctx, exec, survivor, l.currency)
		if err != nil {
			return fmt.Errorf("merge listing %s: %w", l.id, err)
		}
		if target == uuid.Nil {
			// The loser's line has no currency and the survivor has several, so
			// nothing says which of them these names belong to. They go with the
			// listing rather than being filed under a currency nobody stated.
			// Splitting an unknown listing across known ones is adr/0071.
			continue
		}
		idns, err := readListingIdentifiers(ctx, exec, l.id)
		if err != nil {
			return err
		}
		if _, err := exec.ExecContext(ctx, `DELETE FROM instrument_listing_identifiers WHERE listing_id = $1`, l.id); err != nil {
			return fmt.Errorf("delete merged listing identifiers: %w", err)
		}
		for _, idn := range idns {
			_, err := exec.ExecContext(ctx, `
				INSERT INTO instrument_listing_identifiers (listing_id, identifier_type, domain, value, canonical, valid_from, valid_before)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
			`, target, idn.Ref.Type, nullStr(idn.Ref.Domain), idn.Ref.Value, idn.Canonical, nullTime(idn.ValidFrom), nullTime(idn.ValidBefore))
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
		        JOIN instrument_listings l ON l.id = li.listing_id
		        WHERE l.instrument_id = i.id) AS n
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
func (p *Postgres) FindInstrumentByIdentifier(ctx context.Context, identifierType, domain, value string) (string, error) {
	if identifier.NamesAListing(identifierType) {
		instID, _, err := p.FindListingByIdentifier(ctx, identifierType, domain, value)
		return instID, err
	}
	var id uuid.UUID
	var err error
	if domain == "" {
		err = p.q.QueryRowContext(ctx, `
			SELECT instrument_id FROM instrument_identifiers
			WHERE identifier_type = $1 AND domain IS NULL AND value = $2
			ORDER BY valid_before IS NULL DESC, valid_before DESC
			LIMIT 1
		`, identifierType, value).Scan(&id)
	} else {
		err = p.q.QueryRowContext(ctx, `
			SELECT instrument_id FROM instrument_identifiers
			WHERE identifier_type = $1 AND domain = $2 AND value = $3
			ORDER BY valid_before IS NULL DESC, valid_before DESC
			LIMIT 1
		`, identifierType, domain, value).Scan(&id)
	}
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("find instrument by identifier: %w", err)
	}
	return id.String(), nil
}

// FindInstrumentWithMetaByIdentifier implements db.InstrumentDB. It orders by
// validity, and dispatches on grain, for the same reasons
// FindInstrumentByIdentifier does.
func (p *Postgres) FindInstrumentWithMetaByIdentifier(ctx context.Context, identifierType, domain, value string) (string, string, string, string, error) {
	var id uuid.UUID
	var ac, exch, cur string
	var err error
	if identifier.NamesAListing(identifierType) {
		// The metadata still comes off the security. Currency and exchange are
		// listing facts and instruments carries a column for each until 0155
		// retires them; reading the listing's own is what 0154 does at the
		// boundaries that display it.
		if domain == "" {
			err = p.q.QueryRowContext(ctx, `
				SELECT l.instrument_id, COALESCE(i.asset_class, ''), COALESCE(i.exchange_mic, ''), COALESCE(i.currency, '')
				FROM instrument_listing_identifiers li
				JOIN instrument_listings l ON l.id = li.listing_id
				JOIN instruments i ON i.id = l.instrument_id
				WHERE li.identifier_type = $1 AND li.domain IS NULL AND li.value = $2
				ORDER BY li.valid_before IS NULL DESC, li.valid_before DESC
				LIMIT 1
			`, identifierType, value).Scan(&id, &ac, &exch, &cur)
		} else {
			err = p.q.QueryRowContext(ctx, `
				SELECT l.instrument_id, COALESCE(i.asset_class, ''), COALESCE(i.exchange_mic, ''), COALESCE(i.currency, '')
				FROM instrument_listing_identifiers li
				JOIN instrument_listings l ON l.id = li.listing_id
				JOIN instruments i ON i.id = l.instrument_id
				WHERE li.identifier_type = $1 AND li.domain = $2 AND li.value = $3
				ORDER BY li.valid_before IS NULL DESC, li.valid_before DESC
				LIMIT 1
			`, identifierType, domain, value).Scan(&id, &ac, &exch, &cur)
		}
		if err == sql.ErrNoRows {
			return "", "", "", "", nil
		}
		if err != nil {
			return "", "", "", "", fmt.Errorf("find instrument with meta by identifier: %w", err)
		}
		return id.String(), ac, exch, cur, nil
	}
	if domain == "" {
		err = p.q.QueryRowContext(ctx, `
			SELECT ii.instrument_id, COALESCE(i.asset_class, ''), COALESCE(i.exchange_mic, ''), COALESCE(i.currency, '')
			FROM instrument_identifiers ii
			JOIN instruments i ON i.id = ii.instrument_id
			WHERE ii.identifier_type = $1 AND ii.domain IS NULL AND ii.value = $2
			ORDER BY ii.valid_before IS NULL DESC, ii.valid_before DESC
			LIMIT 1
		`, identifierType, value).Scan(&id, &ac, &exch, &cur)
	} else {
		err = p.q.QueryRowContext(ctx, `
			SELECT ii.instrument_id, COALESCE(i.asset_class, ''), COALESCE(i.exchange_mic, ''), COALESCE(i.currency, '')
			FROM instrument_identifiers ii
			JOIN instruments i ON i.id = ii.instrument_id
			WHERE ii.identifier_type = $1 AND ii.domain = $2 AND ii.value = $3
			ORDER BY ii.valid_before IS NULL DESC, ii.valid_before DESC
			LIMIT 1
		`, identifierType, domain, value).Scan(&id, &ac, &exch, &cur)
	}
	if err == sql.ErrNoRows {
		return "", "", "", "", nil
	}
	if err != nil {
		return "", "", "", "", fmt.Errorf("find instrument with meta by identifier: %w", err)
	}
	return id.String(), ac, exch, cur, nil
}

// FindInstrumentByTypeAndValue implements db.InstrumentDB.
// Returns "" if no row matches or if more than one instrument has the same (type, value) with different domains (ambiguous).
// FindInstrumentByTickerIgnoringSeparators implements db.InstrumentDB. The
// separator set matches identifier.NormalizeSplitTicker, and both sides are
// stripped rather than one being rewritten, because an OCC root has lost the
// separator's position and cannot have it put back.
func (p *Postgres) FindInstrumentByTickerIgnoringSeparators(ctx context.Context, value string) (string, error) {
	rows, err := p.q.QueryContext(ctx, `
		SELECT DISTINCT l.instrument_id
		FROM instrument_listing_identifiers li
		JOIN instrument_listings l ON l.id = li.listing_id
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
			SELECT l.instrument_id
			FROM instrument_listing_identifiers li
			JOIN instrument_listings l ON l.id = li.listing_id
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
			JOIN instrument_listings l ON l.id = cl.listing_id
			WHERE l.instrument_id = ii.instrument_id AND cl.canonical
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

// GetInstrument implements db.InstrumentDB.
func (p *Postgres) GetInstrument(ctx context.Context, instrumentID string) (*db.InstrumentRow, error) {
	instUUID, err := uuid.Parse(instrumentID)
	if err != nil {
		return nil, fmt.Errorf("invalid instrument id: %w", err)
	}
	var r instrumentRow
	err = p.q.GetContext(ctx, &r, `
		SELECT i.id, i.asset_class, i.exchange_mic, i.currency, i.name, i.exchange, i.underlying_id, i.valid_from, i.valid_before,
		       i.cik, i.sic_code,
		       i.strike, i.expiry, i.put_call, i.contract_multiplier,
		       e.name AS exchange_name, e.acronym AS exchange_acronym, e.country_code AS exchange_country_code
		FROM instruments i
		LEFT JOIN exchanges e ON e.mic = i.exchange_mic
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
		            JOIN instrument_listings l ON l.id = li.listing_id
		            WHERE l.instrument_id = i.id AND li.canonical = true
		          ))`

	args := []interface{}{}
	argN := 1

	if len(assetClasses) > 0 {
		matched += fmt.Sprintf("\n\t\t\tAND i.asset_class = ANY($%d)", argN)
		args = append(args, pq.Array(assetClasses))
		argN++
	}
	if exchangeFilter != "" {
		matched += fmt.Sprintf("\n\t\t\tAND i.exchange_mic = $%d", argN)
		args = append(args, exchangeFilter)
	}

	base := `
		WITH matched AS (` + matched + `
		), selected AS (
			SELECT id FROM matched
			UNION
			SELECT d.underlying_id FROM instruments d
			JOIN matched m ON m.id = d.id
			WHERE d.underlying_id IS NOT NULL
		)
		SELECT i.id, i.asset_class, i.exchange_mic, i.currency, i.name, i.exchange, i.underlying_id,
		       i.valid_from, i.valid_before, i.cik, i.sic_code,
		       i.strike, i.expiry, i.put_call, i.contract_multiplier,
		       e.name AS exchange_name, e.acronym AS exchange_acronym, e.country_code AS exchange_country_code,
		       u_id.identifier_type AS underlying_identifier_type,
		       u_id.value AS underlying_identifier_value,
		       COALESCE(u_id.domain, '') AS underlying_identifier_domain
		FROM instruments i
		JOIN selected s ON s.id = i.id
		LEFT JOIN exchanges e ON e.mic = i.exchange_mic` +
		bestIdentifierJoinOn("LEFT JOIN", "i.underlying_id", "u_id") + `
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
		SELECT i.id, i.asset_class, i.exchange_mic, i.currency, i.name, i.exchange, i.underlying_id, i.valid_from, i.valid_before,
		       i.cik, i.sic_code,
		       i.strike, i.expiry, i.put_call, i.contract_multiplier,
		       e.name AS exchange_name, e.acronym AS exchange_acronym, e.country_code AS exchange_country_code
		FROM instruments i
		LEFT JOIN exchanges e ON e.mic = i.exchange_mic
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
// answer that produced them. Nothing here reads it yet. The merge below still
// acts on the flat identifier set, which is the union 0140 replaces: a set the
// caller assembled from several results is not an association anybody stated,
// and two results agreeing about a currency and a venue have not said they are
// the same security. Carrying the partition is 0139; acting on it is 0140.
func (p *Postgres) EnsureInstrument(ctx context.Context, assetClass, exchangeMIC, currency, name, cik, sicCode string, identifiers []db.IdentifierInput, claims []db.IdentityClaim, underlyingID string, validFrom, validBefore *time.Time, optionFields *db.OptionFields) (string, string, error) {
	_ = claims
	if len(identifiers) == 0 {
		return "", "", fmt.Errorf("at least one identifier required")
	}
	if assetClass != "" && !db.ValidAssetClasses[assetClass] {
		return "", "", fmt.Errorf("invalid asset_class %q", assetClass)
	}
	if (assetClass == db.AssetClassOption || assetClass == db.AssetClassFuture) && underlyingID == "" {
		return "", "", fmt.Errorf("underlying_id required when asset_class is %s", assetClass)
	}
	// Normalize MIC_TICKER domains and exchangeMIC to operating MICs.
	exchangeMIC = p.normalizeToOperatingMIC(ctx, exchangeMIC)
	for i := range identifiers {
		if identifiers[i].Ref.Type == "MIC_TICKER" && identifiers[i].Ref.Domain != "" {
			identifiers[i].Ref.Domain = p.normalizeToOperatingMIC(ctx, identifiers[i].Ref.Domain)
		}
	}
	var underlyingUUID *uuid.UUID
	if underlyingID != "" {
		parsed, err := uuid.Parse(underlyingID)
		if err != nil {
			return "", "", fmt.Errorf("invalid underlying_id: %w", err)
		}
		underlyingUUID = &parsed
	}
	// Each identifier is stored against what its type names. The split is taken
	// once here and carried through every branch below, so no branch can decide
	// it differently.
	securityIDs, listingIDs := splitByGrain(identifiers)
	// Look up every identifier and collect distinct instrument IDs (no early return).
	seen := make(map[uuid.UUID]struct{})
	var distinctIDs []uuid.UUID
	for _, idn := range identifiers {
		// FindInstrumentByIdentifier asks the table the type names, so a mixed
		// set is looked up a row at a time at each row's own grain.
		existingID, err := p.FindInstrumentByIdentifier(ctx, idn.Ref.Type, idn.Ref.Domain, idn.Ref.Value)
		if err != nil {
			return "", "", fmt.Errorf("lookup instrument: %w", err)
		}
		if existingID != "" {
			parsed, _ := uuid.Parse(existingID)
			if _, ok := seen[parsed]; !ok {
				seen[parsed] = struct{}{}
				distinctIDs = append(distinctIDs, parsed)
			}
		}
	}
	// Multiple instruments: merge into one and return survivor.
	if len(distinctIDs) > 1 {
		survivor, err := pickSurvivor(ctx, p.q, distinctIDs)
		if err != nil {
			return "", "", err
		}
		var listingID uuid.UUID
		err = p.runInTx(ctx, func(exec queryable) error {
			for _, id := range distinctIDs {
				if id == survivor {
					continue
				}
				if err := mergeInstruments(ctx, exec, survivor, id); err != nil {
					return err
				}
			}
			// The survivor's line for the stated currency, which is where the
			// caller's listing-grain identifiers belong. Minting it if it is not
			// there yet is the same act ensureListing performs on the create
			// path, and it is idempotent for a survivor that already has one.
			listingID, err = ensureListing(ctx, exec, survivor, currency)
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
			// The line the currency names, read rather than written. This is the
			// path a bulk import takes on almost every row, so it stays one
			// statement outside a transaction; only a currency the security has
			// no line in yet is worth the write below.
			listingID, err := listingFor(ctx, p.q, id, currency)
			if err != nil {
				return "", "", err
			}
			if listingID == uuid.Nil && currency != "" {
				err = p.runInTx(ctx, func(exec queryable) error {
					var lErr error
					listingID, lErr = ensureListing(ctx, exec, id, currency)
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
			var mErr error
			listingID, mErr = mergeIntoInstrument(ctx, exec, id, db.InstrumentMerge{
				AssetClass:  assetClass,
				ExchangeMIC: exchangeMIC,
				Currency:    currency,
				CIK:         cik,
				SICCode:     sicCode,
				ValidFrom:   validFrom,
				ValidBefore: validBefore,
				Identifiers: identifiers,
			})
			if mErr != nil {
				return mErr
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
			INSERT INTO instruments (asset_class, exchange_mic, currency, name, cik, sic_code, underlying_id, valid_from, valid_before, strike, expiry, put_call)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			RETURNING id
		`, nullStr(assetClass), nullStr(exchangeMIC), nullStr(currency), nullStr(name), nullStr(cik), nullStr(sicCode), nullUUID(underlyingUUID), nullTime(validFrom), nullTime(validBefore), strike, expiry, putCall).Scan(&newID)
		if err != nil {
			return err
		}
		// Every security has at least one currency line, and a security created
		// without a stated currency gets the unknown one rather than none: how
		// many lines it has is what is unknown, not whether it has any.
		newListingID, err = ensureListing(ctx, exec, newID, currency)
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
		// A listing-grain name goes on the line the currency named. A security
		// just created has exactly one, so newListingID is never nil here.
		for _, idn := range listingIDs {
			_, err = exec.ExecContext(ctx, `INSERT INTO instrument_listing_identifiers (listing_id, identifier_type, domain, value, canonical, valid_from, valid_before) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				newListingID, idn.Ref.Type, nullStr(idn.Ref.Domain), idn.Ref.Value, idn.Canonical, nullTime(idn.ValidFrom), nullTime(idn.ValidBefore))
			if err != nil {
				if isIdentifierConflict(err) {
					return errIdentifierExists // rollback tx; caller will look up existing id
				}
				return err
			}
		}
		return nil
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
func holdsNoCanonicalIdentifier(ctx context.Context, exec queryable, id uuid.UUID) (bool, error) {
	var exists bool
	err := exec.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM instrument_identifiers WHERE instrument_id = $1 AND canonical)
		    OR EXISTS (
		         SELECT 1 FROM instrument_listing_identifiers li
		         JOIN instrument_listings l ON l.id = li.listing_id
		         WHERE l.instrument_id = $1 AND li.canonical
		       )
	`, id).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check canonical identifiers: %w", err)
	}
	return !exists, nil
}

// updateInstrumentOnMatch optionally sets underlying_id and option fields on an
// existing instrument. It writes no identifier, which is what leaves each name's
// valid_from where it was: matching an existing instrument is not evidence that
// any of its names became correct today, and moving them is what used to disarm
// the retroactive option-split guard.
//
// An instrument holding no identity at all is the exception, handled by its
// caller: an absent name is inserted with the vintage the resolution stamped on
// it (adr/0055), which is a different act from moving one that is already there.
func updateInstrumentOnMatch(ctx context.Context, exec queryable, id uuid.UUID, underlyingID *uuid.UUID, optionFields *db.OptionFields) error {
	if optionFields != nil {
		_, err := exec.ExecContext(ctx, `
			UPDATE instruments SET underlying_id = COALESCE($2, underlying_id), strike = $3, expiry = $4, put_call = $5
			WHERE id = $1
		`, id, nullUUID(underlyingID), optionFields.Strike, optionFields.Expiry, optionFields.PutCall)
		return err
	}
	_, err := exec.ExecContext(ctx, `UPDATE instruments SET underlying_id = COALESCE($2, underlying_id) WHERE id = $1`, id, nullUUID(underlyingID))
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
		"i.id", "i.asset_class", "i.exchange_mic", "i.currency", "i.name", "i.exchange", "i.underlying_id", "i.valid_from", "i.valid_before",
		"i.cik", "i.sic_code",
		"i.strike", "i.expiry", "i.put_call", "i.contract_multiplier",
		"e.name AS exchange_name", "e.acronym AS exchange_acronym", "e.country_code AS exchange_country_code",
	).
		From("instruments i").
		LeftJoin("exchanges e ON e.mic = i.exchange_mic").
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
// A listing-grain row needs the listing said outright rather than inferred. The
// caller holds it -- EnsureInstrument hands it back -- and a security with two
// lines has no way to work out which one a bare ticker meant.
func (p *Postgres) InsertInstrumentIdentifier(ctx context.Context, instrumentID, listingID string, input db.IdentifierInput) error {
	if input.Ref.Type == "MIC_TICKER" && input.Ref.Domain != "" {
		input.Ref.Domain = p.normalizeToOperatingMIC(ctx, input.Ref.Domain)
	}
	if identifier.NamesAListing(input.Ref.Type) {
		lid, err := uuid.Parse(listingID)
		if err != nil {
			return fmt.Errorf("insert listing identifier %s: invalid listing id %q: %w", input.Ref.Type, listingID, err)
		}
		_, err = p.q.ExecContext(ctx, `
			INSERT INTO instrument_listing_identifiers (listing_id, identifier_type, domain, value, canonical, valid_from, valid_before)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, lid, input.Ref.Type, nullStr(input.Ref.Domain), input.Ref.Value, input.Canonical, nullTime(input.ValidFrom), nullTime(input.ValidBefore))
		if err != nil {
			return fmt.Errorf("insert listing identifier: %w", err)
		}
		return nil
	}
	uid, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("insert instrument identifier: invalid id: %w", err)
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
	mic := p.normalizeToOperatingMIC(ctx, in.ExchangeMIC)
	idns := make([]db.IdentifierInput, len(in.Identifiers))
	copy(idns, in.Identifiers)
	for i := range idns {
		if idns[i].Ref.Type == "MIC_TICKER" && idns[i].Ref.Domain != "" {
			idns[i].Ref.Domain = p.normalizeToOperatingMIC(ctx, idns[i].Ref.Domain)
		}
	}
	in.ExchangeMIC = mic
	in.Identifiers = idns
	return p.runInTx(ctx, func(exec queryable) error {
		_, err := mergeIntoInstrument(ctx, exec, uid, in)
		return err
	})
}

// mergeIntoInstrument adds what an instrument does not already have: identifiers
// it lacks, and columns that are still NULL. A stored value always wins
// (adr/0004), so it fills blanks and never rewrites.
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
func mergeIntoInstrument(ctx context.Context, exec queryable, id uuid.UUID, in db.InstrumentMerge) (uuid.UUID, error) {
	securityIDs, listingIDs := splitByGrain(in.Identifiers)
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
			return uuid.Nil, fmt.Errorf("merge identifier (%s/%s): %w", idn.Ref.Type, idn.Ref.Value, err)
		}
	}
	// The WHERE guard leaves a row that needs nothing unwritten, which keeps
	// naming exchange_mic in the SET list from firing the name recompute on
	// every instrument in the file.
	_, err := exec.ExecContext(ctx, `
		UPDATE instruments SET
			asset_class = COALESCE(asset_class, $2),
			exchange_mic = COALESCE(exchange_mic, $3),
			currency = COALESCE(currency, $4),
			cik = COALESCE(cik, $5),
			sic_code = COALESCE(sic_code, $6),
			valid_from = COALESCE(valid_from, $7),
			valid_before = COALESCE(valid_before, $8)
		WHERE id = $1
		  AND (asset_class IS NULL OR exchange_mic IS NULL OR currency IS NULL
		       OR cik IS NULL OR sic_code IS NULL
		       OR valid_from IS NULL OR valid_before IS NULL)
	`, id, nullStr(in.AssetClass), nullStr(in.ExchangeMIC), nullStr(in.Currency),
		nullStr(in.CIK), nullStr(in.SICCode), nullTime(in.ValidFrom), nullTime(in.ValidBefore))
	if err != nil {
		return uuid.Nil, fmt.Errorf("merge instrument columns: %w", err)
	}
	// A currency filling a blank names the line the security was already
	// trading on, so its unknown listing moves onto that currency rather than
	// gaining a sibling. The guard against rewriting a stored value lives in
	// ensureListing: a security already quoted in this family keeps what it has.
	listingID, err := ensureListing(ctx, exec, id, in.Currency)
	if err != nil {
		return uuid.Nil, err
	}
	// The listing-grain names go on last, because the listing they go on is what
	// the two statements above have just settled. A file that named none leaves
	// nothing to do here, which is why an ambiguous listing is only an error
	// where there is actually something to file under it.
	if len(listingIDs) == 0 {
		return listingID, nil
	}
	if listingID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("merge listing identifiers: no currency stated and the security has several lines")
	}
	for _, idn := range listingIDs {
		_, err := exec.ExecContext(ctx, `
			INSERT INTO instrument_listing_identifiers (listing_id, identifier_type, domain, value, canonical, valid_from, valid_before)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT DO NOTHING
		`, listingID, idn.Ref.Type, nullStr(idn.Ref.Domain), idn.Ref.Value, idn.Canonical, nullTime(idn.ValidFrom), nullTime(idn.ValidBefore))
		if err != nil {
			return uuid.Nil, fmt.Errorf("merge listing identifier (%s/%s): %w", idn.Ref.Type, idn.Ref.Value, err)
		}
	}
	return listingID, nil
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
// Routed by grain, exactly as the canonical identifiers are. Every provider type
// that exists today names a listing, so listingID is required in practice; it is
// still only required where there is a listing-grain row to file, so a caller
// holding none is not made to produce one.
func (p *Postgres) SaveProviderIdentifiers(ctx context.Context, instrumentID, listingID string, ids []db.ProviderIdentifierInput) error {
	if len(ids) == 0 {
		return nil
	}
	securityPIs, listingPIs := splitProviderByGrain(ids)
	if len(securityPIs) > 0 {
		uid, err := uuid.Parse(instrumentID)
		if err != nil {
			return fmt.Errorf("save provider identifiers: invalid id: %w", err)
		}
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
	lid, err := uuid.Parse(listingID)
	if err != nil {
		return fmt.Errorf("save provider identifiers: invalid listing id %q: %w", listingID, err)
	}
	for _, id := range listingPIs {
		_, err := p.q.ExecContext(ctx, `
			INSERT INTO provider_listing_identifiers (listing_id, provider, identifier_type, domain, value)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT DO NOTHING
		`, lid, id.Provider, id.Type, nullStr(id.Domain), id.Value)
		if err != nil {
			return fmt.Errorf("save provider listing identifier (%s/%s): %w", id.Provider, id.Type, err)
		}
	}
	return nil
}

// FindProviderIdentifiers implements db.InstrumentDB.
//
// Both grains, because the caller is asking what it may key a request on and
// that is a question about the security and every line of it. This is a
// flattening at the boundary in the sense InstrumentRow.AllIdentifiers is, not a
// lookup: nothing here is searching two tables for one row. Narrowing the
// question to one listing is 0148 for prices and 0150 for corporate events.
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
		JOIN instrument_listings l ON l.id = pli.listing_id
		WHERE l.instrument_id = $1 AND pli.provider = $2
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
