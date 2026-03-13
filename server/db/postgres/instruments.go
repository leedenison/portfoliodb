package postgres

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

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
	// Build placeholders for IN clause to avoid pq.Array uuid handling
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = ids[i]
	}
	inClause := strings.Join(placeholders, ",")
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
	var row db.InstrumentRow
	row.ID = instrumentID
	var assetClass, exchange, currency, name sql.NullString
	var underlyingID sql.NullString
	var validFrom, validTo sql.NullTime
	err = p.q.QueryRowContext(ctx, `SELECT asset_class, exchange, currency, name, underlying_id, valid_from, valid_to FROM instruments WHERE id = $1`, instUUID).
		Scan(&assetClass, &exchange, &currency, &name, &underlyingID, &validFrom, &validTo)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get instrument: %w", err)
	}
	row.AssetClass = strFromNull(assetClass)
	row.Exchange = strFromNull(exchange)
	row.Currency = strFromNull(currency)
	row.Name = strFromNull(name)
	row.UnderlyingID = strFromNull(underlyingID)
	row.ValidFrom = timeFromNull(validFrom)
	row.ValidTo = timeFromNull(validTo)
	idRows, err := p.q.QueryContext(ctx, `SELECT identifier_type, domain, value, canonical FROM instrument_identifiers WHERE instrument_id = $1`, instUUID)
	if err != nil {
		return nil, fmt.Errorf("get instrument identifiers: %w", err)
	}
	defer idRows.Close()
	for idRows.Next() {
		var idn db.IdentifierInput
		var domain sql.NullString
		if err := idRows.Scan(&idn.Type, &domain, &idn.Value, &idn.Canonical); err != nil {
			return nil, err
		}
		if domain.Valid {
			idn.Domain = domain.String
		}
		row.Identifiers = append(row.Identifiers, idn)
	}
	if err := idRows.Err(); err != nil {
		return nil, err
	}
	return &row, nil
}

// ListInstrumentsForExport implements db.InstrumentDB.
func (p *Postgres) ListInstrumentsForExport(ctx context.Context, exchangeFilter string) ([]*db.InstrumentRow, error) {
	var rows *sql.Rows
	var err error
	if exchangeFilter != "" {
		rows, err = p.q.QueryContext(ctx, `
			SELECT i.id, i.asset_class, i.exchange, i.currency, i.name, i.underlying_id, i.valid_from, i.valid_to
			FROM instruments i
			WHERE EXISTS (SELECT 1 FROM instrument_identifiers ii WHERE ii.instrument_id = i.id AND ii.canonical = true)
			AND i.exchange = $1
			ORDER BY i.id
		`, exchangeFilter)
	} else {
		rows, err = p.q.QueryContext(ctx, `
			SELECT i.id, i.asset_class, i.exchange, i.currency, i.name, i.underlying_id, i.valid_from, i.valid_to
			FROM instruments i
			WHERE EXISTS (SELECT 1 FROM instrument_identifiers ii WHERE ii.instrument_id = i.id AND ii.canonical = true)
			ORDER BY i.id
		`)
	}
	if err != nil {
		return nil, fmt.Errorf("list instruments for export: %w", err)
	}
	defer rows.Close()
	var results []*db.InstrumentRow
	var ids []uuid.UUID
	for rows.Next() {
		var row db.InstrumentRow
		var id uuid.UUID
		var assetClass, exchange, currency, name sql.NullString
		var underlyingID sql.NullString
		var validFrom, validTo sql.NullTime
		if err := rows.Scan(&id, &assetClass, &exchange, &currency, &name, &underlyingID, &validFrom, &validTo); err != nil {
			return nil, err
		}
		row.ID = id.String()
		row.AssetClass = strFromNull(assetClass)
		row.Exchange = strFromNull(exchange)
		row.Currency = strFromNull(currency)
		row.Name = strFromNull(name)
		row.UnderlyingID = strFromNull(underlyingID)
		row.ValidFrom = timeFromNull(validFrom)
		row.ValidTo = timeFromNull(validTo)
		results = append(results, &row)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return results, nil
	}
	// Load all identifiers for these instruments (build placeholders to avoid pq.Array uuid handling).
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = ids[i]
	}
	idRows, err := p.q.QueryContext(ctx, fmt.Sprintf(`
		SELECT instrument_id, identifier_type, domain, value, canonical
		FROM instrument_identifiers
		WHERE instrument_id IN (%s)
		ORDER BY instrument_id
	`, strings.Join(placeholders, ",")), args...)
	if err != nil {
		return nil, fmt.Errorf("list identifiers for export: %w", err)
	}
	defer idRows.Close()
	byID := make(map[string]*db.InstrumentRow)
	for _, r := range results {
		byID[r.ID] = r
	}
	for idRows.Next() {
		var instID uuid.UUID
		var idType, val string
		var domain sql.NullString
		var canonical bool
		if err := idRows.Scan(&instID, &idType, &domain, &val, &canonical); err != nil {
			return nil, err
		}
		idn := db.IdentifierInput{Type: idType, Value: val, Canonical: canonical}
		if domain.Valid {
			idn.Domain = domain.String
		}
		row := byID[instID.String()]
		if row != nil {
			row.Identifiers = append(row.Identifiers, idn)
		}
	}
	if err := idRows.Err(); err != nil {
		return nil, err
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
	placeholders := make([]string, len(uuids))
	args := make([]interface{}, len(uuids))
	for i := range uuids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = uuids[i]
	}
	rows, err := p.q.QueryContext(ctx, fmt.Sprintf(`
		SELECT i.id, i.asset_class, i.exchange, i.currency, i.name, i.underlying_id, i.valid_from, i.valid_to
		FROM instruments i WHERE i.id IN (%s)
	`, strings.Join(placeholders, ",")), args...)
	if err != nil {
		return nil, fmt.Errorf("list instruments by ids: %w", err)
	}
	defer rows.Close()
	var results []*db.InstrumentRow
	var resultIDs []uuid.UUID
	for rows.Next() {
		var row db.InstrumentRow
		var id uuid.UUID
		var assetClass, exchange, currency, name sql.NullString
		var underlyingID sql.NullString
		var validFrom, validTo sql.NullTime
		if err := rows.Scan(&id, &assetClass, &exchange, &currency, &name, &underlyingID, &validFrom, &validTo); err != nil {
			return nil, err
		}
		row.ID = id.String()
		row.AssetClass = strFromNull(assetClass)
		row.Exchange = strFromNull(exchange)
		row.Currency = strFromNull(currency)
		row.Name = strFromNull(name)
		row.UnderlyingID = strFromNull(underlyingID)
		row.ValidFrom = timeFromNull(validFrom)
		row.ValidTo = timeFromNull(validTo)
		results = append(results, &row)
		resultIDs = append(resultIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(resultIDs) == 0 {
		return results, nil
	}
	// Load identifiers for these instruments.
	idPlaceholders := make([]string, len(resultIDs))
	idArgs := make([]interface{}, len(resultIDs))
	for i := range resultIDs {
		idPlaceholders[i] = fmt.Sprintf("$%d", i+1)
		idArgs[i] = resultIDs[i]
	}
	idRows, err := p.q.QueryContext(ctx, fmt.Sprintf(`
		SELECT instrument_id, identifier_type, domain, value, canonical
		FROM instrument_identifiers WHERE instrument_id IN (%s) ORDER BY instrument_id
	`, strings.Join(idPlaceholders, ",")), idArgs...)
	if err != nil {
		return nil, fmt.Errorf("list identifiers for instruments: %w", err)
	}
	defer idRows.Close()
	byID := make(map[string]*db.InstrumentRow)
	for _, r := range results {
		byID[r.ID] = r
	}
	for idRows.Next() {
		var instID uuid.UUID
		var idType, val string
		var domain sql.NullString
		var canonical bool
		if err := idRows.Scan(&instID, &idType, &domain, &val, &canonical); err != nil {
			return nil, err
		}
		idn := db.IdentifierInput{Type: idType, Value: val, Canonical: canonical}
		if domain.Valid {
			idn.Domain = domain.String
		}
		if row := byID[instID.String()]; row != nil {
			row.Identifiers = append(row.Identifiers, idn)
		}
	}
	return results, idRows.Err()
}

// EnsureInstrument implements db.InstrumentDB.
// Finds by any identifier; if not found, creates instrument and inserts identifiers.
// When multiple identifiers resolve to different instruments, merges them eagerly and returns the survivor.
// On unique violation (identifier already exists for another instrument), returns the existing instrument ID (eager merge).
func (p *Postgres) EnsureInstrument(ctx context.Context, assetClass, exchange, currency, name string, identifiers []db.IdentifierInput, underlyingID string, validFrom, validTo *time.Time) (string, error) {
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
			return nil
		})
		if err != nil {
			return "", err
		}
		return survivor.String(), nil
	}
	// Exactly one instrument: return it.
	if len(distinctIDs) == 1 {
		return distinctIDs[0].String(), nil
	}
	// None found: create new instrument and add identifiers.
	var newID uuid.UUID
	err := p.runInTx(ctx, func(exec queryable) error {
		err := exec.QueryRowContext(ctx, `
			INSERT INTO instruments (asset_class, exchange, currency, name, underlying_id, valid_from, valid_to)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id
		`, nullStr(assetClass), nullStr(exchange), nullStr(currency), nullStr(name), nullUUID(underlyingUUID), nullTime(validFrom), nullTime(validTo)).Scan(&newID)
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

// ListInstruments implements db.InstrumentDB.
func (p *Postgres) ListInstruments(ctx context.Context, search string, assetClasses []string, pageSize int32, pageToken string) ([]*db.InstrumentRow, int32, string, error) {
	limit := pageSize
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	var offset int64
	if pageToken != "" {
		b, err := base64.StdEncoding.DecodeString(pageToken)
		if err == nil {
			offset, _ = strconv.ParseInt(string(b), 10, 64)
		}
	}

	// Display name: prefer TICKER, then instrument.name, then BROKER_DESCRIPTION, then id::text.
	displayName := `COALESCE(
		(SELECT ii.value FROM instrument_identifiers ii WHERE ii.instrument_id = i.id AND ii.identifier_type = 'TICKER' ORDER BY ii.domain, ii.value LIMIT 1),
		NULLIF(i.name, ''),
		(SELECT ii.value FROM instrument_identifiers ii WHERE ii.instrument_id = i.id AND ii.identifier_type = 'BROKER_DESCRIPTION' ORDER BY ii.domain, ii.value LIMIT 1),
		i.id::text
	)`

	// Build WHERE clauses for optional filters.
	var conditions []string
	var args []interface{}
	argIdx := 1
	if search != "" {
		conditions = append(conditions, fmt.Sprintf(`(%s) ILIKE '%%' || $%d || '%%'`, displayName, argIdx))
		args = append(args, search)
		argIdx++
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
		var parts []string
		if len(filtered) > 0 {
			placeholders := make([]string, len(filtered))
			for i, ac := range filtered {
				placeholders[i] = fmt.Sprintf("$%d", argIdx)
				args = append(args, ac)
				argIdx++
			}
			parts = append(parts, fmt.Sprintf("i.asset_class IN (%s)", strings.Join(placeholders, ",")))
		}
		if includeEmpty {
			parts = append(parts, "(i.asset_class IS NULL OR i.asset_class = '')")
		}
		conditions = append(conditions, "("+strings.Join(parts, " OR ")+")")
	}
	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Count total matching instruments.
	var total int32
	countQ := fmt.Sprintf(`SELECT COUNT(*) FROM instruments i %s`, where)
	if err := p.q.QueryRowContext(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, "", fmt.Errorf("count instruments: %w", err)
	}
	if total == 0 {
		return nil, 0, "", nil
	}

	q := fmt.Sprintf(`
		SELECT i.id, i.asset_class, i.exchange, i.currency, i.name, i.underlying_id, i.valid_from, i.valid_to
		FROM instruments i
		%s
		ORDER BY lower(%s)
		LIMIT $%d OFFSET $%d
	`, where, displayName, argIdx, argIdx+1)
	args = append(args, limit+1, offset)

	rows, err := p.q.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, "", fmt.Errorf("list instruments: %w", err)
	}
	defer rows.Close()
	var results []*db.InstrumentRow
	var ids []uuid.UUID
	for rows.Next() {
		var row db.InstrumentRow
		var id uuid.UUID
		var assetClass, exchange, currency, name sql.NullString
		var underlyingID sql.NullString
		var validFrom, validTo sql.NullTime
		if err := rows.Scan(&id, &assetClass, &exchange, &currency, &name, &underlyingID, &validFrom, &validTo); err != nil {
			return nil, 0, "", err
		}
		row.ID = id.String()
		row.AssetClass = strFromNull(assetClass)
		row.Exchange = strFromNull(exchange)
		row.Currency = strFromNull(currency)
		row.Name = strFromNull(name)
		row.UnderlyingID = strFromNull(underlyingID)
		row.ValidFrom = timeFromNull(validFrom)
		row.ValidTo = timeFromNull(validTo)
		results = append(results, &row)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, "", err
	}

	// Compute next page token (we fetched limit+1 to detect more pages).
	var nextToken string
	if int32(len(results)) > limit {
		results = results[:limit]
		ids = ids[:limit]
		nextOffset := offset + int64(limit)
		nextToken = base64.StdEncoding.EncodeToString([]byte(strconv.FormatInt(nextOffset, 10)))
	}

	if len(ids) == 0 {
		return results, total, nextToken, nil
	}
	// Batch-load identifiers.
	placeholders := make([]string, len(ids))
	idArgs := make([]interface{}, len(ids))
	for i := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		idArgs[i] = ids[i]
	}
	idRows, err := p.q.QueryContext(ctx, fmt.Sprintf(`
		SELECT instrument_id, identifier_type, domain, value, canonical
		FROM instrument_identifiers
		WHERE instrument_id IN (%s)
		ORDER BY instrument_id
	`, strings.Join(placeholders, ",")), idArgs...)
	if err != nil {
		return nil, 0, "", fmt.Errorf("list instrument identifiers: %w", err)
	}
	defer idRows.Close()
	byID := make(map[string]*db.InstrumentRow)
	for _, r := range results {
		byID[r.ID] = r
	}
	for idRows.Next() {
		var instID uuid.UUID
		var idType, val string
		var domain sql.NullString
		var canonical bool
		if err := idRows.Scan(&instID, &idType, &domain, &val, &canonical); err != nil {
			return nil, 0, "", err
		}
		idn := db.IdentifierInput{Type: idType, Value: val, Canonical: canonical}
		if domain.Valid {
			idn.Domain = domain.String
		}
		row := byID[instID.String()]
		if row != nil {
			row.Identifiers = append(row.Identifiers, idn)
		}
	}
	if err := idRows.Err(); err != nil {
		return nil, 0, "", err
	}
	return results, total, nextToken, nil
}
