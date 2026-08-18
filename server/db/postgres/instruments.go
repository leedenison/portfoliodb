package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"
)

// errIdentifierExists is returned when EnsureInstrument hits a unique violation (identifier already for another instrument).
var errIdentifierExists = errors.New("identifier already exists for another instrument")

// mergeInstruments merges mergedAway into survivor inside the same transaction: updates all txs pointing at mergedAway to survivor, moves identifier rows to survivor (or keeps survivor's if duplicate), then deletes mergedAway. exec must be a transaction.
// The delete is deliberate and lossy: mergedAway's canonical fields and its cascaded prices, splits, dividends and coverage rows go with it, and nothing records the prior identity. See docs/adr/0004-instrument-resolution-and-merge.md.
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
		if err := rows.Scan(&idn.Type, &domain, &idn.Value, &idn.Canonical, &validFrom, &validBefore); err != nil {
			return err
		}
		if domain.Valid {
			idn.Domain = domain.String
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
		`, survivor, idn.Type, nullStr(idn.Domain), idn.Value, idn.Canonical, nullTime(idn.ValidFrom), nullTime(idn.ValidBefore))
		if err != nil {
			if isIdentifierConflict(err) {
				continue
			}
			return fmt.Errorf("insert identifier: %w", err)
		}
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

// pickSurvivor returns the instrument ID that should survive when merging the given set (most identifiers, then oldest created_at). ids must have at least one element.
func pickSurvivor(ctx context.Context, q queryable, ids []uuid.UUID) (uuid.UUID, error) {
	if len(ids) == 0 {
		return uuid.Nil, fmt.Errorf("pickSurvivor requires at least one id")
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	inClause, args := inClauseUUIDs(ids)
	query := fmt.Sprintf(`
		SELECT i.id, i.created_at, (SELECT count(*) FROM instrument_identifiers WHERE instrument_id = i.id) AS n
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
	// Sort by n desc, created_at asc (more identifiers wins, then older wins)
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].n != cands[j].n {
			return cands[i].n > cands[j].n
		}
		return cands[i].createdAt.Before(cands[j].createdAt)
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
func (p *Postgres) FindInstrumentByIdentifier(ctx context.Context, identifierType, domain, value string) (string, error) {
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
// validity for the same reason FindInstrumentByIdentifier does.
func (p *Postgres) FindInstrumentWithMetaByIdentifier(ctx context.Context, identifierType, domain, value string) (string, string, string, string, error) {
	var id uuid.UUID
	var ac, exch, cur string
	var err error
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
		SELECT DISTINCT instrument_id FROM instrument_identifiers
		WHERE identifier_type = 'MIC_TICKER'
		  AND translate(value, './- ', '') = translate($1, './- ', '')
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
	rows, err := p.q.QueryContext(ctx, `
		SELECT instrument_id FROM instrument_identifiers
		WHERE identifier_type = $1 AND value = $2
	`, identifierType, value)
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
		       i.strike, i.expiry, i.put_call, i.contract_multiplier, i.identity_as_of,
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
		WHERE EXISTS (SELECT 1 FROM instrument_identifiers ii WHERE ii.instrument_id = i.id AND ii.canonical = true)`

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
		       i.strike, i.expiry, i.put_call, i.contract_multiplier, i.identity_as_of,
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
		       i.strike, i.expiry, i.put_call, i.contract_multiplier, i.identity_as_of,
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
	return results, nil
}

// EnsureInstrument implements db.InstrumentDB.
// Finds by any identifier; if not found, creates instrument and inserts identifiers.
// When multiple identifiers resolve to different instruments, merges them eagerly and returns the survivor.
// On unique violation (identifier already exists for another instrument), returns the existing instrument ID (eager merge).
func (p *Postgres) EnsureInstrument(ctx context.Context, assetClass, exchangeMIC, currency, name, cik, sicCode string, identifiers []db.IdentifierInput, underlyingID string, validFrom, validBefore *time.Time, optionFields *db.OptionFields) (string, error) {
	if len(identifiers) == 0 {
		return "", fmt.Errorf("at least one identifier required")
	}
	if assetClass != "" && !db.ValidAssetClasses[assetClass] {
		return "", fmt.Errorf("invalid asset_class %q", assetClass)
	}
	if (assetClass == db.AssetClassOption || assetClass == db.AssetClassFuture) && underlyingID == "" {
		return "", fmt.Errorf("underlying_id required when asset_class is %s", assetClass)
	}
	// Normalize MIC_TICKER domains and exchangeMIC to operating MICs.
	exchangeMIC = p.normalizeToOperatingMIC(ctx, exchangeMIC)
	for i := range identifiers {
		if identifiers[i].Type == "MIC_TICKER" && identifiers[i].Domain != "" {
			identifiers[i].Domain = p.normalizeToOperatingMIC(ctx, identifiers[i].Domain)
		}
	}
	var underlyingUUID *uuid.UUID
	if underlyingID != "" {
		parsed, err := uuid.Parse(underlyingID)
		if err != nil {
			return "", fmt.Errorf("invalid underlying_id: %w", err)
		}
		underlyingUUID = &parsed
	}
	// Look up every identifier and collect distinct instrument IDs (no early return).
	seen := make(map[uuid.UUID]struct{})
	var distinctIDs []uuid.UUID
	for _, idn := range identifiers {
		existingID, err := p.FindInstrumentByIdentifier(ctx, idn.Type, idn.Domain, idn.Value)
		if err != nil {
			return "", fmt.Errorf("lookup instrument: %w", err)
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
			return "", err
		}
		err = p.runInTx(ctx, func(exec queryable) error {
			for _, id := range distinctIDs {
				if id == survivor {
					continue
				}
				if err := mergeInstruments(ctx, exec, survivor, id); err != nil {
					return err
				}
			}
			// Update underlying and option fields on the survivor.
			return updateInstrumentOnMatch(ctx, exec, survivor, underlyingUUID, optionFields)
		})
		if err != nil {
			return "", err
		}
		return survivor.String(), nil
	}
	// Exactly one instrument: update underlying and option fields.
	if len(distinctIDs) == 1 {
		id := distinctIDs[0]
		if err := updateInstrumentOnMatch(ctx, p.q, id, underlyingUUID, optionFields); err != nil {
			return "", err
		}
		return id.String(), nil
	}
	// None found: create new instrument and add identifiers.
	var newID uuid.UUID
	err := p.runInTx(ctx, func(exec queryable) error {
		var strike, expiry, putCall any
		if optionFields != nil {
			strike = optionFields.Strike
			expiry = optionFields.Expiry
			putCall = optionFields.PutCall
		}
		// identity_as_of is left NULL: creating a row is not evidence that its
		// identity reflects any particular market state. The plugin resolution
		// path stamps it explicitly once identification has actually succeeded.
		err := exec.QueryRowContext(ctx, `
			INSERT INTO instruments (asset_class, exchange_mic, currency, name, cik, sic_code, underlying_id, valid_from, valid_before, strike, expiry, put_call)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			RETURNING id
		`, nullStr(assetClass), nullStr(exchangeMIC), nullStr(currency), nullStr(name), nullStr(cik), nullStr(sicCode), nullUUID(underlyingUUID), nullTime(validFrom), nullTime(validBefore), strike, expiry, putCall).Scan(&newID)
		if err != nil {
			return err
		}
		for _, idn := range identifiers {
			canonical := idn.Canonical
			_, err = exec.ExecContext(ctx, `INSERT INTO instrument_identifiers (instrument_id, identifier_type, domain, value, canonical, valid_from, valid_before) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				newID, idn.Type, nullStr(idn.Domain), idn.Value, canonical, nullTime(idn.ValidFrom), nullTime(idn.ValidBefore))
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
			for _, idn := range identifiers {
				existingID, rowErr := p.FindInstrumentByIdentifier(ctx, idn.Type, idn.Domain, idn.Value)
				if rowErr == nil && existingID != "" {
					return existingID, nil
				}
			}
		}
		return "", err
	}
	return newID.String(), nil
}

// updateInstrumentOnMatch optionally sets underlying_id and option fields on an
// existing instrument. It deliberately leaves identity_as_of alone: matching an
// existing instrument is not a re-derivation of its identity, and bumping the
// column here is what used to disarm the retroactive option-split guard. Callers
// that genuinely re-derive identity call UpdateIdentityAsOf themselves.
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
		"i.strike", "i.expiry", "i.put_call", "i.contract_multiplier", "i.identity_as_of",
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
func (p *Postgres) InsertInstrumentIdentifier(ctx context.Context, instrumentID string, input db.IdentifierInput) error {
	uid, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("insert instrument identifier: invalid id: %w", err)
	}
	if input.Type == "MIC_TICKER" && input.Domain != "" {
		input.Domain = p.normalizeToOperatingMIC(ctx, input.Domain)
	}
	_, err = p.q.ExecContext(ctx, `
		INSERT INTO instrument_identifiers (instrument_id, identifier_type, domain, value, canonical, valid_from, valid_before)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, uid, input.Type, nullStr(input.Domain), input.Value, input.Canonical, nullTime(input.ValidFrom), nullTime(input.ValidBefore))
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
		if idns[i].Type == "MIC_TICKER" && idns[i].Domain != "" {
			idns[i].Domain = p.normalizeToOperatingMIC(ctx, idns[i].Domain)
		}
	}
	return p.runInTx(ctx, func(exec queryable) error {
		for _, idn := range idns {
			// ON CONFLICT with no target covers the exclusion constraint as well as
			// a unique index, so an identifier the row already holds over an
			// overlapping interval is still a no-op rather than an error.
			_, err := exec.ExecContext(ctx, `
				INSERT INTO instrument_identifiers (instrument_id, identifier_type, domain, value, canonical, valid_from, valid_before)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				ON CONFLICT DO NOTHING
			`, uid, idn.Type, nullStr(idn.Domain), idn.Value, idn.Canonical, nullTime(idn.ValidFrom), nullTime(idn.ValidBefore))
			if err != nil {
				return fmt.Errorf("merge identifier (%s/%s): %w", idn.Type, idn.Value, err)
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
		`, uid, nullStr(in.AssetClass), nullStr(mic), nullStr(in.Currency),
			nullStr(in.CIK), nullStr(in.SICCode), nullTime(in.ValidFrom), nullTime(in.ValidBefore))
		if err != nil {
			return fmt.Errorf("merge instrument columns: %w", err)
		}
		return nil
	})
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

// UpdateIdentityAsOf implements db.InstrumentDB.
func (p *Postgres) UpdateIdentityAsOf(ctx context.Context, instrumentID string) error {
	uid, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("update identity_as_of: invalid id: %w", err)
	}
	_, err = p.q.ExecContext(ctx, `UPDATE instruments SET identity_as_of = now() WHERE id = $1`, uid)
	if err != nil {
		return fmt.Errorf("update identity_as_of: %w", err)
	}
	return nil
}

// SetIdentityAsOf implements db.InstrumentDB.
// The column only ever moves forward. A caller supplying a vintage cannot know
// whether EnsureInstrument created the row or matched an existing one, and
// dragging the stamp backwards onto an already-adjusted option would re-expose
// it to the retroactive split pass. GREATEST ignores a NULL left-hand side, so
// an unstamped row takes the supplied value.
func (p *Postgres) SetIdentityAsOf(ctx context.Context, instrumentID string, t time.Time) error {
	uid, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("set identity_as_of: invalid id: %w", err)
	}
	_, err = p.q.ExecContext(ctx, `UPDATE instruments SET identity_as_of = GREATEST(identity_as_of, $2) WHERE id = $1`, uid, t)
	if err != nil {
		return fmt.Errorf("set identity_as_of: %w", err)
	}
	return nil
}

// SaveProviderIdentifiers implements db.InstrumentDB.
func (p *Postgres) SaveProviderIdentifiers(ctx context.Context, instrumentID string, ids []db.ProviderIdentifierInput) error {
	if len(ids) == 0 {
		return nil
	}
	uid, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("save provider identifiers: invalid id: %w", err)
	}
	for _, id := range ids {
		_, err := p.q.ExecContext(ctx, `
			INSERT INTO provider_instrument_identifiers (instrument_id, provider, identifier_type, domain, value)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT DO NOTHING
		`, uid, id.Provider, id.Type, nullStr(id.Domain), id.Value)
		if err != nil {
			return fmt.Errorf("save provider identifier (%s/%s): %w", id.Provider, id.Type, err)
		}
	}
	return nil
}

// FindProviderIdentifiers implements db.InstrumentDB.
func (p *Postgres) FindProviderIdentifiers(ctx context.Context, instrumentID, provider string) ([]db.ProviderIdentifierInput, error) {
	uid, err := uuid.Parse(instrumentID)
	if err != nil {
		return nil, fmt.Errorf("find provider identifiers: invalid id: %w", err)
	}
	rows, err := p.q.QueryContext(ctx, `
		SELECT provider, identifier_type, domain, value
		FROM provider_instrument_identifiers
		WHERE instrument_id = $1 AND provider = $2
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
