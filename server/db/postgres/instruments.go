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
	"github.com/lib/pq"
	"github.com/leedenison/portfoliodb/server/db"
)

// errIdentifierExists is returned when EnsureInstrument hits a unique violation (identifier already for another instrument).
var errIdentifierExists = errors.New("identifier already exists for another instrument")

// mergeInstruments merges mergedAway into survivor inside the same transaction: updates all txs pointing at mergedAway to survivor, moves identifier rows to survivor (or keeps survivor's if duplicate), then deletes mergedAway. exec must be a transaction.
func mergeInstruments(ctx context.Context, exec queryable, survivor, mergedAway uuid.UUID) error {
	if survivor == mergedAway {
		return nil
	}
	if _, err := exec.ExecContext(ctx, `UPDATE txs SET instrument_id = $1 WHERE instrument_id = $2`, survivor, mergedAway); err != nil {
		return fmt.Errorf("update txs: %w", err)
	}
	rows, err := exec.QueryContext(ctx, `SELECT identifier_type, domain, value, canonical FROM instrument_identifiers WHERE instrument_id = $1`, mergedAway)
	if err != nil {
		return fmt.Errorf("list identifiers: %w", err)
	}
	defer rows.Close()
	var toInsert []struct{ idType, domain, value string; canonical bool }
	for rows.Next() {
		var idType, val string
		var domain sql.NullString
		var canonical bool
		if err := rows.Scan(&idType, &domain, &val, &canonical); err != nil {
			return err
		}
		d := ""
		if domain.Valid {
			d = domain.String
		}
		toInsert = append(toInsert, struct{ idType, domain, value string; canonical bool }{idType, d, val, canonical})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := exec.ExecContext(ctx, `DELETE FROM instrument_identifiers WHERE instrument_id = $1`, mergedAway); err != nil {
		return fmt.Errorf("delete merged identifiers: %w", err)
	}
	for _, idn := range toInsert {
		_, err := exec.ExecContext(ctx, `
			INSERT INTO instrument_identifiers (instrument_id, identifier_type, domain, value, canonical) VALUES ($1, $2, $3, $4, $5)
		`, survivor, idn.idType, nullStr(idn.domain), idn.value, idn.canonical)
		if err != nil {
			if isUniqueViolation(err) {
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

// FindInstrumentByIdentifier implements db.InstrumentDB.
func (p *Postgres) FindInstrumentByIdentifier(ctx context.Context, identifierType, domain, value string) (string, error) {
	var id uuid.UUID
	var err error
	if domain == "" {
		err = p.q.QueryRowContext(ctx, `
			SELECT instrument_id FROM instrument_identifiers
			WHERE identifier_type = $1 AND domain IS NULL AND value = $2
		`, identifierType, value).Scan(&id)
	} else {
		err = p.q.QueryRowContext(ctx, `
			SELECT instrument_id FROM instrument_identifiers
			WHERE identifier_type = $1 AND domain = $2 AND value = $3
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

// FindInstrumentByTypeAndValue implements db.InstrumentDB.
// Returns "" if no row matches or if more than one instrument has the same (type, value) with different domains (ambiguous).
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
		SELECT i.id, i.asset_class, i.exchange_mic, i.currency, i.name, i.exchange, i.underlying_id, i.valid_from, i.valid_to,
		       i.cik, i.sic_code,
		       i.strike, i.expiry, i.put_call, i.contract_multiplier, i.identified_at,
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
	if err := loadIdentifiers(ctx, p.q, []uuid.UUID{r.ID}, []*db.InstrumentRow{row}); err != nil {
		return nil, fmt.Errorf("get instrument identifiers: %w", err)
	}
	return row, nil
}

// ListInstrumentsForExport implements db.InstrumentDB.
func (p *Postgres) ListInstrumentsForExport(ctx context.Context, exchangeFilter string) ([]*db.InstrumentRow, error) {
	var irows []instrumentRow
	var err error
	if exchangeFilter != "" {
		err = p.q.SelectContext(ctx, &irows, `
			SELECT i.id, i.asset_class, i.exchange_mic, i.currency, i.name, i.exchange, i.underlying_id, i.valid_from, i.valid_to,
			       e.name AS exchange_name, e.acronym AS exchange_acronym, e.country_code AS exchange_country_code
			FROM instruments i
			LEFT JOIN exchanges e ON e.mic = i.exchange_mic
			WHERE EXISTS (SELECT 1 FROM instrument_identifiers ii WHERE ii.instrument_id = i.id AND ii.canonical = true)
			AND i.exchange_mic = $1
			ORDER BY i.id
		`, exchangeFilter)
	} else {
		err = p.q.SelectContext(ctx, &irows, `
			SELECT i.id, i.asset_class, i.exchange_mic, i.currency, i.name, i.exchange, i.underlying_id, i.valid_from, i.valid_to,
			       e.name AS exchange_name, e.acronym AS exchange_acronym, e.country_code AS exchange_country_code
			FROM instruments i
			LEFT JOIN exchanges e ON e.mic = i.exchange_mic
			WHERE EXISTS (SELECT 1 FROM instrument_identifiers ii WHERE ii.instrument_id = i.id AND ii.canonical = true)
			ORDER BY i.id
		`)
	}
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
		SELECT i.id, i.asset_class, i.exchange_mic, i.currency, i.name, i.exchange, i.underlying_id, i.valid_from, i.valid_to,
		       i.cik, i.sic_code,
		       i.strike, i.expiry, i.put_call, i.contract_multiplier, i.identified_at,
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
	return results, nil
}

// EnsureInstrument implements db.InstrumentDB.
// Finds by any identifier; if not found, creates instrument and inserts identifiers.
// When multiple identifiers resolve to different instruments, merges them eagerly and returns the survivor.
// On unique violation (identifier already exists for another instrument), returns the existing instrument ID (eager merge).
func (p *Postgres) EnsureInstrument(ctx context.Context, assetClass, exchangeMIC, currency, name, cik, sicCode string, identifiers []db.IdentifierInput, underlyingID string, validFrom, validTo *time.Time, optionFields *db.OptionFields) (string, error) {
	if len(identifiers) == 0 {
		return "", fmt.Errorf("at least one identifier required")
	}
	if assetClass != "" && !db.ValidAssetClasses[assetClass] {
		return "", fmt.Errorf("invalid asset_class %q", assetClass)
	}
	if (assetClass == db.AssetClassOption || assetClass == db.AssetClassFuture) && underlyingID == "" {
		return "", fmt.Errorf("underlying_id required when asset_class is %s", assetClass)
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
			// Update identified_at and option fields on survivor.
			return updateIdentifiedAt(ctx, exec, survivor, optionFields)
		})
		if err != nil {
			return "", err
		}
		return survivor.String(), nil
	}
	// Exactly one instrument: update identified_at and option fields.
	if len(distinctIDs) == 1 {
		id := distinctIDs[0]
		if err := updateIdentifiedAt(ctx, p.q, id, optionFields); err != nil {
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
		err := exec.QueryRowContext(ctx, `
			INSERT INTO instruments (asset_class, exchange_mic, currency, name, cik, sic_code, underlying_id, valid_from, valid_to, strike, expiry, put_call, identified_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, now())
			RETURNING id
		`, nullStr(assetClass), nullStr(exchangeMIC), nullStr(currency), nullStr(name), nullStr(cik), nullStr(sicCode), nullUUID(underlyingUUID), nullTime(validFrom), nullTime(validTo), strike, expiry, putCall).Scan(&newID)
		if err != nil {
			return err
		}
		for _, idn := range identifiers {
			canonical := idn.Canonical
			_, err = exec.ExecContext(ctx, `INSERT INTO instrument_identifiers (instrument_id, identifier_type, domain, value, canonical) VALUES ($1, $2, $3, $4, $5)`, newID, idn.Type, nullStr(idn.Domain), idn.Value, canonical)
			if err != nil {
				if isUniqueViolation(err) {
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

// updateIdentifiedAt sets identified_at = now() and optionally updates option
// fields on an existing instrument.
func updateIdentifiedAt(ctx context.Context, exec queryable, id uuid.UUID, optionFields *db.OptionFields) error {
	if optionFields != nil {
		_, err := exec.ExecContext(ctx, `
			UPDATE instruments SET identified_at = now(), strike = $2, expiry = $3, put_call = $4
			WHERE id = $1
		`, id, optionFields.Strike, optionFields.Expiry, optionFields.PutCall)
		return err
	}
	_, err := exec.ExecContext(ctx, `UPDATE instruments SET identified_at = now() WHERE id = $1`, id)
	return err
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
		"i.id", "i.asset_class", "i.exchange_mic", "i.currency", "i.name", "i.exchange", "i.underlying_id", "i.valid_from", "i.valid_to",
		"i.cik", "i.sic_code",
		"i.strike", "i.expiry", "i.put_call", "i.contract_multiplier", "i.identified_at",
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

// ListOptionsByUnderlying implements db.InstrumentDB.
func (p *Postgres) ListOptionsByUnderlying(ctx context.Context, underlyingID string) ([]*db.InstrumentRow, error) {
	uid, err := uuid.Parse(underlyingID)
	if err != nil {
		return nil, fmt.Errorf("list options by underlying: invalid id: %w", err)
	}
	var irows []instrumentRow
	err = p.q.SelectContext(ctx, &irows, `
		SELECT i.id, i.asset_class, i.exchange_mic, i.currency, i.name, i.exchange, i.underlying_id, i.valid_from, i.valid_to,
		       i.cik, i.sic_code,
		       i.strike, i.expiry, i.put_call, i.contract_multiplier, i.identified_at,
		       e.name AS exchange_name, e.acronym AS exchange_acronym, e.country_code AS exchange_country_code
		FROM instruments i
		LEFT JOIN exchanges e ON e.mic = i.exchange_mic
		WHERE i.underlying_id = $1 AND i.asset_class = 'OPTION'
		ORDER BY i.id
	`, uid)
	if err != nil {
		return nil, fmt.Errorf("list options by underlying: %w", err)
	}
	results := make([]*db.InstrumentRow, len(irows))
	ids := make([]uuid.UUID, len(irows))
	for i := range irows {
		results[i] = irows[i].toDBRow()
		ids[i] = irows[i].ID
	}
	if err := loadIdentifiers(ctx, p.q, ids, results); err != nil {
		return nil, fmt.Errorf("list options by underlying identifiers: %w", err)
	}
	return results, nil
}

// DeleteInstrumentIdentifier implements db.InstrumentDB.
func (p *Postgres) DeleteInstrumentIdentifier(ctx context.Context, instrumentID, identifierType, value string) error {
	uid, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("delete instrument identifier: invalid id: %w", err)
	}
	_, err = p.q.ExecContext(ctx, `
		DELETE FROM instrument_identifiers
		WHERE instrument_id = $1 AND identifier_type = $2 AND value = $3
	`, uid, identifierType, value)
	if err != nil {
		return fmt.Errorf("delete instrument identifier: %w", err)
	}
	return nil
}

// InsertInstrumentIdentifier implements db.InstrumentDB.
func (p *Postgres) InsertInstrumentIdentifier(ctx context.Context, instrumentID string, input db.IdentifierInput) error {
	uid, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("insert instrument identifier: invalid id: %w", err)
	}
	_, err = p.q.ExecContext(ctx, `
		INSERT INTO instrument_identifiers (instrument_id, identifier_type, domain, value, canonical)
		VALUES ($1, $2, $3, $4, $5)
	`, uid, input.Type, nullStr(input.Domain), input.Value, input.Canonical)
	if err != nil {
		return fmt.Errorf("insert instrument identifier: %w", err)
	}
	return nil
}

// UpdateInstrumentStrike implements db.InstrumentDB.
func (p *Postgres) UpdateInstrumentStrike(ctx context.Context, instrumentID string, strike float64) error {
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
