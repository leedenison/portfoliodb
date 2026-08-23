package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// ensureListing gives an instrument the listing it must have: a security has at
// least one currency line, and everything downstream of this level leans on
// that. It either mints one or moves the security's unknown listing onto a
// currency it has just learned, and returns the line the currency names.
//
// currency == "" means the currency is unknown, which is a different claim from
// "this security has one line and it is X". The unknown listing is never
// priceable and never event-bearing, and moving it onto a currency later is a
// relabelling rather than a loss, which is what makes the split in adr/0071
// possible.
//
// The returned id is uuid.Nil only where no currency was stated and the security
// has more than one line. Nothing said which, and picking one would file the
// caller's listing-grain identifiers under a currency nobody stated -- the same
// refusal ListingForVenue makes when a bare MIC matches two lines.
//
// Idempotent, and safe to call for an instrument that already has listings: a
// security that already holds this currency family keeps the listing it has,
// including the code it is quoted in. GBX does not become GBP.
func ensureListing(ctx context.Context, exec queryable, instrumentID uuid.UUID, currency string) (uuid.UUID, error) {
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
			return uuid.Nil, fmt.Errorf("ensure unknown listing: %w", err)
		}
		return soleListing(ctx, exec, instrumentID)
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
		return uuid.Nil, fmt.Errorf("name unknown listing: %w", err)
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
		return uuid.Nil, fmt.Errorf("ensure listing %s: %w", currency, err)
	}
	// The two statements above have just guaranteed the line exists, so this
	// reads it back rather than looking for it.
	return listingFor(ctx, exec, instrumentID, currency)
}

// listingFor reads the line a currency names, writing nothing.
//
// uuid.Nil means there is no answer to give: the security has no line in that
// currency family, or no currency was stated and it has more than one line. A
// caller that needs the line to exist follows this with ensureListing; one that
// only needs to know which line was named -- which is every match on an
// instrument that already exists -- is spared the write and the transaction
// around it, and that is the whole of a bulk import's hot path.
func listingFor(ctx context.Context, exec queryable, instrumentID uuid.UUID, currency string) (uuid.UUID, error) {
	if currency == "" {
		return soleListing(ctx, exec, instrumentID)
	}
	// The family, not the code: a line stored in GBX is the line GBP names.
	var id uuid.UUID
	err := exec.QueryRowContext(ctx, `
		SELECT id FROM instrument_listings
		WHERE instrument_id = $1 AND currency IS NOT NULL
		  AND currency_family(currency) = currency_family($2)
	`, instrumentID, currency).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("read listing %s: %w", currency, err)
	}
	return id, nil
}

// soleListing returns the security's only listing, or uuid.Nil where it has
// several. A caller reaching this stated no currency, so it has named no line,
// and the answer is only unambiguous while there is one line to name.
func soleListing(ctx context.Context, exec queryable, instrumentID uuid.UUID) (uuid.UUID, error) {
	rows, err := exec.QueryContext(ctx, `
		SELECT id FROM instrument_listings WHERE instrument_id = $1 LIMIT 2
	`, instrumentID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("read listings: %w", err)
	}
	defer rows.Close()
	var id uuid.UUID
	n := 0
	for rows.Next() {
		var next uuid.UUID
		if err := rows.Scan(&next); err != nil {
			return uuid.Nil, fmt.Errorf("scan listing: %w", err)
		}
		n++
		id = next
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, err
	}
	if n != 1 {
		return uuid.Nil, nil
	}
	return id, nil
}

// requireCurrencyBearingListing rejects a listing that no strike could be read
// against: one that does not exist, and the security's currency-unknown line.
//
// A contract's strike is a price and a price is in a currency, so an underlying
// whose currency is unknown leaves the strike denominated in nothing. adr/0068
// already says an unknown listing is not event-bearing; this is the same claim
// reaching the derivative written on it. See
// docs/adr/0074-an-options-underlying-is-the-line-its-strike-is-quoted-in.md.
func requireCurrencyBearingListing(ctx context.Context, exec queryable, listingID uuid.UUID) error {
	var currency sql.NullString
	err := exec.QueryRowContext(ctx,
		`SELECT currency FROM instrument_listings WHERE id = $1`, listingID).Scan(&currency)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("underlying listing %s does not exist", listingID)
	}
	if err != nil {
		return fmt.Errorf("read underlying listing %s: %w", listingID, err)
	}
	if !currency.Valid {
		return fmt.Errorf("underlying listing %s states no currency, so the strike is denominated in nothing", listingID)
	}
	return nil
}

// FindListingByIdentifier implements db.ListingDB.
//
// It orders by validity for the reason FindInstrumentByIdentifier gives: where
// retained history leaves more than one row, the name in force now wins and the
// most recently closed one is the fallback.
func (p *Postgres) FindListingByIdentifier(ctx context.Context, identifierType, domain, value string) (string, string, error) {
	if !identifier.NamesAListing(identifierType) {
		return "", "", fmt.Errorf("find listing by identifier: %s names a security, not a listing", identifierType)
	}
	var instID, listingID uuid.UUID
	var err error
	if domain == "" {
		err = p.q.QueryRowContext(ctx, `
			SELECT l.instrument_id, l.id
			FROM instrument_listing_identifiers li
			JOIN instrument_listings l ON l.id = li.listing_id
			WHERE li.identifier_type = $1 AND li.domain IS NULL AND li.value = $2
			ORDER BY li.valid_before IS NULL DESC, li.valid_before DESC
			LIMIT 1
		`, identifierType, value).Scan(&instID, &listingID)
	} else {
		err = p.q.QueryRowContext(ctx, `
			SELECT l.instrument_id, l.id
			FROM instrument_listing_identifiers li
			JOIN instrument_listings l ON l.id = li.listing_id
			WHERE li.identifier_type = $1 AND li.domain = $2 AND li.value = $3
			ORDER BY li.valid_before IS NULL DESC, li.valid_before DESC
			LIMIT 1
		`, identifierType, domain, value).Scan(&instID, &listingID)
	}
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("find listing by identifier: %w", err)
	}
	return instID.String(), listingID.String(), nil
}

// ListingForVenue implements db.ListingDB.
//
// Two matches is unresolved rather than a choice: the LSE lists both the GBP and
// the USD line of one ETC, so a bare MIC does not always name a line, and
// settling it by picking one would attach a holding to a currency nobody stated.
func (p *Postgres) ListingForVenue(ctx context.Context, instrumentID, mic string) (string, error) {
	if mic == "" {
		return "", nil
	}
	instUUID, err := uuid.Parse(instrumentID)
	if err != nil {
		return "", fmt.Errorf("listing for venue: invalid instrument id %q: %w", instrumentID, err)
	}
	rows, err := p.q.QueryContext(ctx, `
		SELECT v.listing_id
		FROM listing_venues v
		JOIN instrument_listings l ON l.id = v.listing_id
		WHERE l.instrument_id = $1 AND v.mic = $2
		LIMIT 2
	`, instUUID, mic)
	if err != nil {
		return "", fmt.Errorf("listing for venue: %w", err)
	}
	defer rows.Close()
	var id uuid.UUID
	n := 0
	for rows.Next() {
		var next uuid.UUID
		if err := rows.Scan(&next); err != nil {
			return "", fmt.Errorf("listing for venue: %w", err)
		}
		n++
		id = next
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if n != 1 {
		return "", nil
	}
	return id.String(), nil
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
	if err := loadListingDetail(ctx, p.q, listings); err != nil {
		return nil, err
	}
	for _, l := range listings {
		out[l.InstrumentID] = append(out[l.InstrumentID], l)
	}
	return out, nil
}

// FindListing implements db.ListingDB.
func (p *Postgres) FindListing(ctx context.Context, instrumentID, currency string) (string, error) {
	id, err := uuid.Parse(instrumentID)
	if err != nil {
		return "", fmt.Errorf("find listing: invalid instrument id %q: %w", instrumentID, err)
	}
	listingID, err := listingFor(ctx, p.q, id, currency)
	if err != nil {
		return "", err
	}
	return nilUUIDToString(listingID), nil
}

// EnsureListing implements db.ListingDB.
func (p *Postgres) EnsureListing(ctx context.Context, instrumentID, currency string) (string, error) {
	id, err := uuid.Parse(instrumentID)
	if err != nil {
		return "", fmt.Errorf("ensure listing: invalid instrument id %q: %w", instrumentID, err)
	}
	var listingID uuid.UUID
	err = p.runInTx(ctx, func(exec queryable) error {
		var err error
		listingID, err = ensureListing(ctx, exec, id, currency)
		return err
	})
	if err != nil {
		return "", err
	}
	return nilUUIDToString(listingID), nil
}

// ListingsByIDs implements db.ListingDB.
func (p *Postgres) ListingsByIDs(ctx context.Context, listingIDs []string) (map[string]*db.Listing, error) {
	out := make(map[string]*db.Listing, len(listingIDs))
	if len(listingIDs) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, 0, len(listingIDs))
	for _, s := range listingIDs {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("listings by id: invalid id %q: %w", s, err)
		}
		ids = append(ids, id)
	}
	listings, err := queryListingsBy(ctx, p.q, "id", ids)
	if err != nil {
		return nil, err
	}
	if err := loadListingDetail(ctx, p.q, listings); err != nil {
		return nil, err
	}
	for _, l := range listings {
		out[l.ID] = l
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
	return loadListingDetail(ctx, q, listings)
}

// loadListingDetail attaches what hangs off a listing: the identifiers that name
// it, the provider identifiers at the same grain, and the venues derived from
// them. Three queries over the whole batch rather than three per listing, in the
// pattern loadIdentifiers follows.
func loadListingDetail(ctx context.Context, q queryable, listings []*db.Listing) error {
	if len(listings) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(listings))
	byID := make(map[string]*db.Listing, len(listings))
	for _, l := range listings {
		id, err := uuid.Parse(l.ID)
		if err != nil {
			return fmt.Errorf("listing detail: invalid id %q: %w", l.ID, err)
		}
		ids = append(ids, id)
		byID[l.ID] = l
	}
	inClause, args := inClauseUUIDs(ids)

	idRows, err := q.QueryContext(ctx, fmt.Sprintf(`
		SELECT listing_id, identifier_type, domain, value, canonical, valid_from, valid_before
		FROM instrument_listing_identifiers
		WHERE listing_id IN (%s)
		ORDER BY listing_id, valid_before IS NULL DESC, valid_before DESC
	`, inClause), args...)
	if err != nil {
		return fmt.Errorf("query listing identifiers: %w", err)
	}
	defer idRows.Close()
	for idRows.Next() {
		var lid uuid.UUID
		var domain sql.NullString
		var validFrom, validBefore sql.NullTime
		var idn db.IdentifierInput
		if err := idRows.Scan(&lid, &idn.Ref.Type, &domain, &idn.Ref.Value, &idn.Canonical, &validFrom, &validBefore); err != nil {
			return fmt.Errorf("scan listing identifier: %w", err)
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
		if l := byID[lid.String()]; l != nil {
			l.Identifiers = append(l.Identifiers, idn)
		}
	}
	if err := idRows.Err(); err != nil {
		return err
	}

	piRows, err := q.QueryContext(ctx, fmt.Sprintf(`
		SELECT listing_id, provider, identifier_type, domain, value
		FROM provider_listing_identifiers
		WHERE listing_id IN (%s)
		ORDER BY listing_id, identifier_type, value
	`, inClause), args...)
	if err != nil {
		return fmt.Errorf("query listing provider identifiers: %w", err)
	}
	defer piRows.Close()
	for piRows.Next() {
		var lid uuid.UUID
		var domain sql.NullString
		var pi db.ProviderIdentifierInput
		if err := piRows.Scan(&lid, &pi.Provider, &pi.Type, &domain, &pi.Value); err != nil {
			return fmt.Errorf("scan listing provider identifier: %w", err)
		}
		if domain.Valid {
			pi.Domain = domain.String
		}
		if l := byID[lid.String()]; l != nil {
			l.ProviderIdentifiers = append(l.ProviderIdentifiers, pi)
		}
	}
	if err := piRows.Err(); err != nil {
		return err
	}

	vRows, err := q.QueryContext(ctx, fmt.Sprintf(`
		SELECT listing_id, mic FROM listing_venues
		WHERE listing_id IN (%s)
		ORDER BY listing_id, mic
	`, inClause), args...)
	if err != nil {
		return fmt.Errorf("query listing venues: %w", err)
	}
	defer vRows.Close()
	for vRows.Next() {
		var lid uuid.UUID
		var mic string
		if err := vRows.Scan(&lid, &mic); err != nil {
			return fmt.Errorf("scan listing venue: %w", err)
		}
		if l := byID[lid.String()]; l != nil {
			l.Venues = append(l.Venues, mic)
		}
	}
	return vRows.Err()
}

// queryListings reads the listings of the given instruments in a stable order:
// a security's known lines first, by currency, then its unknown one. Nothing
// makes a listing primary -- naming one would reintroduce the
// default-versus-unknown conflation this level removes -- so the order is for
// reproducibility and not a ranking.
func queryListings(ctx context.Context, q queryable, ids []uuid.UUID) ([]*db.Listing, error) {
	return queryListingsBy(ctx, q, "instrument_id", ids)
}

// queryListingsBy is queryListings keyed on col, which is instrument_id for a
// caller holding securities and id for one holding listings. The price fetcher
// is the second kind: its unit of work is the line, and it arrives holding
// nothing else.
func queryListingsBy(ctx context.Context, q queryable, col string, ids []uuid.UUID) ([]*db.Listing, error) {
	inClause, args := inClauseUUIDs(ids)
	rows, err := q.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, instrument_id, currency, valid_from, valid_before, created_at
		FROM instrument_listings
		WHERE %s IN (%s)
		ORDER BY instrument_id, currency IS NULL, currency
	`, col, inClause), args...)
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
