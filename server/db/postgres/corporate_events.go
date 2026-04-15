package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/leedenison/portfoliodb/server/db"
)

// UpsertStockSplits implements db.CorporateEventDB.
func (p *Postgres) UpsertStockSplits(ctx context.Context, splits []db.StockSplit) error {
	if len(splits) == 0 {
		return nil
	}
	instIDs := make([]string, len(splits))
	exDates := make([]time.Time, len(splits))
	froms := make([]string, len(splits))
	tos := make([]string, len(splits))
	providers := make([]string, len(splits))
	for i, s := range splits {
		instIDs[i] = s.InstrumentID
		exDates[i] = s.ExDate
		froms[i] = s.SplitFrom
		tos[i] = s.SplitTo
		providers[i] = s.DataProvider
	}
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO stock_splits (instrument_id, ex_date, split_from, split_to, data_provider, fetched_at)
		SELECT unnest($1::uuid[]), unnest($2::date[]),
			unnest($3::numeric[]), unnest($4::numeric[]),
			unnest($5::text[]), now()
		ON CONFLICT (instrument_id, ex_date) DO UPDATE SET
			split_from    = EXCLUDED.split_from,
			split_to      = EXCLUDED.split_to,
			data_provider = EXCLUDED.data_provider
	`, pq.Array(instIDs), pq.Array(exDates), pq.Array(froms), pq.Array(tos), pq.Array(providers))
	if err != nil {
		return fmt.Errorf("upsert stock splits: %w", err)
	}
	return nil
}

// ListStockSplits implements db.CorporateEventDB.
func (p *Postgres) ListStockSplits(ctx context.Context, instrumentID string) ([]db.StockSplit, error) {
	id, err := uuid.Parse(instrumentID)
	if err != nil {
		return nil, fmt.Errorf("list stock splits: invalid instrument id %q: %w", instrumentID, err)
	}
	rows, err := p.q.QueryContext(ctx, `
		SELECT instrument_id, ex_date, split_from::text, split_to::text, data_provider, fetched_at
		FROM stock_splits
		WHERE instrument_id = $1
		ORDER BY ex_date
	`, id)
	if err != nil {
		return nil, fmt.Errorf("list stock splits: %w", err)
	}
	defer rows.Close()
	var out []db.StockSplit
	for rows.Next() {
		var s db.StockSplit
		var instUUID uuid.UUID
		if err := rows.Scan(&instUUID, &s.ExDate, &s.SplitFrom, &s.SplitTo, &s.DataProvider, &s.FetchedAt); err != nil {
			return nil, fmt.Errorf("list stock splits scan: %w", err)
		}
		s.InstrumentID = instUUID.String()
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteStockSplit implements db.CorporateEventDB.
func (p *Postgres) DeleteStockSplit(ctx context.Context, instrumentID string, exDate time.Time) error {
	_, err := p.q.ExecContext(ctx, `
		DELETE FROM stock_splits WHERE instrument_id = $1 AND ex_date = $2
	`, instrumentID, exDate)
	if err != nil {
		return fmt.Errorf("delete stock split: %w", err)
	}
	return nil
}

// UpsertCashDividends implements db.CorporateEventDB.
func (p *Postgres) UpsertCashDividends(ctx context.Context, dividends []db.CashDividend) error {
	if len(dividends) == 0 {
		return nil
	}
	instIDs := make([]string, len(dividends))
	exDates := make([]time.Time, len(dividends))
	payDates := make([]*time.Time, len(dividends))
	recordDates := make([]*time.Time, len(dividends))
	declDates := make([]*time.Time, len(dividends))
	amounts := make([]string, len(dividends))
	currencies := make([]string, len(dividends))
	frequencies := make([]sql.NullString, len(dividends))
	types := make([]string, len(dividends))
	providers := make([]string, len(dividends))
	for i, d := range dividends {
		instIDs[i] = d.InstrumentID
		exDates[i] = d.ExDate
		payDates[i] = d.PayDate
		recordDates[i] = d.RecordDate
		declDates[i] = d.DeclarationDate
		amounts[i] = d.Amount
		currencies[i] = d.Currency
		if d.Frequency != "" {
			frequencies[i] = sql.NullString{String: d.Frequency, Valid: true}
		}
		types[i] = d.Type
		if types[i] == "" {
			types[i] = "CD"
		}
		providers[i] = d.DataProvider
	}
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO cash_dividends (
			instrument_id, ex_date, pay_date, record_date, declaration_date,
			amount, currency, frequency, type, data_provider, fetched_at
		)
		SELECT unnest($1::uuid[]), unnest($2::date[]),
			unnest($3::date[]), unnest($4::date[]), unnest($5::date[]),
			unnest($6::numeric[]), unnest($7::text[]), unnest($8::text[]),
			unnest($9::text[]), unnest($10::text[]), now()
		ON CONFLICT (instrument_id, ex_date) DO UPDATE SET
			pay_date         = EXCLUDED.pay_date,
			record_date      = EXCLUDED.record_date,
			declaration_date = EXCLUDED.declaration_date,
			amount           = EXCLUDED.amount,
			currency         = EXCLUDED.currency,
			frequency        = EXCLUDED.frequency,
			type             = EXCLUDED.type,
			data_provider    = EXCLUDED.data_provider,
			fetched_at       = EXCLUDED.fetched_at
	`, pq.Array(instIDs), pq.Array(exDates),
		pq.Array(payDates), pq.Array(recordDates), pq.Array(declDates),
		pq.Array(amounts), pq.Array(currencies), pq.Array(frequencies),
		pq.Array(types), pq.Array(providers))
	if err != nil {
		return fmt.Errorf("upsert cash dividends: %w", err)
	}
	return nil
}

// ListCashDividends implements db.CorporateEventDB.
func (p *Postgres) ListCashDividends(ctx context.Context, instrumentID string) ([]db.CashDividend, error) {
	id, err := uuid.Parse(instrumentID)
	if err != nil {
		return nil, fmt.Errorf("list cash dividends: invalid instrument id %q: %w", instrumentID, err)
	}
	rows, err := p.q.QueryContext(ctx, `
		SELECT instrument_id, ex_date, pay_date, record_date, declaration_date,
			amount::text, currency, frequency, type, data_provider, fetched_at
		FROM cash_dividends
		WHERE instrument_id = $1
		ORDER BY ex_date
	`, id)
	if err != nil {
		return nil, fmt.Errorf("list cash dividends: %w", err)
	}
	defer rows.Close()
	var out []db.CashDividend
	for rows.Next() {
		var d db.CashDividend
		var instUUID uuid.UUID
		var pay, record, decl sql.NullTime
		var freq sql.NullString
		if err := rows.Scan(&instUUID, &d.ExDate, &pay, &record, &decl,
			&d.Amount, &d.Currency, &freq, &d.Type, &d.DataProvider, &d.FetchedAt); err != nil {
			return nil, fmt.Errorf("list cash dividends scan: %w", err)
		}
		d.InstrumentID = instUUID.String()
		if pay.Valid {
			t := pay.Time
			d.PayDate = &t
		}
		if record.Valid {
			t := record.Time
			d.RecordDate = &t
		}
		if decl.Valid {
			t := decl.Time
			d.DeclarationDate = &t
		}
		if freq.Valid {
			d.Frequency = freq.String
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteCashDividend implements db.CorporateEventDB.
func (p *Postgres) DeleteCashDividend(ctx context.Context, instrumentID string, exDate time.Time) error {
	_, err := p.q.ExecContext(ctx, `
		DELETE FROM cash_dividends WHERE instrument_id = $1 AND ex_date = $2
	`, instrumentID, exDate)
	if err != nil {
		return fmt.Errorf("delete cash dividend: %w", err)
	}
	return nil
}

// UpsertCorporateEventCoverage implements db.CorporateEventDB. The merge step
// finds every existing row for (instrument, plugin) whose interval is adjacent
// to or overlaps with [from, to], deletes them, and inserts a single row
// spanning the union. Two intervals are adjacent when one ends the day before
// the other begins. The whole operation runs in a single transaction so
// concurrent inserts cannot leave partial state.
func (p *Postgres) UpsertCorporateEventCoverage(ctx context.Context, instrumentID, pluginID string, from, to time.Time) error {
	if to.Before(from) {
		return fmt.Errorf("upsert corporate event coverage: covered_to %s before covered_from %s", to, from)
	}
	id, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("upsert corporate event coverage: invalid instrument id %q: %w", instrumentID, err)
	}
	return p.runInTx(ctx, func(exec queryable) error {
		// Two intervals [a,b] and [c,d] are adjacent or overlap iff
		// a <= d+1 AND c <= b+1. Find every existing row that touches
		// [from, to] under that rule, compute the union, delete them, and
		// insert one merged row. CTE data-modifying statements share a
		// snapshot in PostgreSQL so DELETE and INSERT cannot see one
		// another -- run them as separate statements inside the transaction.
		var newFrom, newTo time.Time
		err := exec.QueryRowContext(ctx, `
			SELECT
				LEAST($3::date,  COALESCE(MIN(covered_from), $3::date)),
				GREATEST($4::date, COALESCE(MAX(covered_to),   $4::date))
			FROM corporate_event_coverage
			WHERE instrument_id = $1
			  AND plugin_id     = $2
			  AND covered_from <= ($4::date + 1)
			  AND covered_to   >= ($3::date - 1)
		`, id, pluginID, from, to).Scan(&newFrom, &newTo)
		if err != nil {
			return fmt.Errorf("upsert corporate event coverage: compute merge: %w", err)
		}
		if _, err := exec.ExecContext(ctx, `
			DELETE FROM corporate_event_coverage
			WHERE instrument_id = $1
			  AND plugin_id     = $2
			  AND covered_from <= ($4::date + 1)
			  AND covered_to   >= ($3::date - 1)
		`, id, pluginID, from, to); err != nil {
			return fmt.Errorf("upsert corporate event coverage: delete overlapping: %w", err)
		}
		if _, err := exec.ExecContext(ctx, `
			INSERT INTO corporate_event_coverage (instrument_id, plugin_id, covered_from, covered_to, fetched_at)
			VALUES ($1, $2, $3, $4, now())
		`, id, pluginID, newFrom, newTo); err != nil {
			return fmt.Errorf("upsert corporate event coverage: insert merged: %w", err)
		}
		return nil
	})
}

// ListCorporateEventCoverage implements db.CorporateEventDB.
func (p *Postgres) ListCorporateEventCoverage(ctx context.Context, instrumentIDs []string) ([]db.CorporateEventCoverage, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if len(instrumentIDs) == 0 {
		rows, err = p.q.QueryContext(ctx, `
			SELECT instrument_id, plugin_id, covered_from, covered_to, fetched_at
			FROM corporate_event_coverage
			ORDER BY instrument_id, plugin_id, covered_from
		`)
	} else {
		uuids := make([]uuid.UUID, 0, len(instrumentIDs))
		for _, s := range instrumentIDs {
			u, err := uuid.Parse(s)
			if err != nil {
				return nil, fmt.Errorf("list corporate event coverage: invalid instrument id %q: %w", s, err)
			}
			uuids = append(uuids, u)
		}
		rows, err = p.q.QueryContext(ctx, `
			SELECT instrument_id, plugin_id, covered_from, covered_to, fetched_at
			FROM corporate_event_coverage
			WHERE instrument_id = ANY($1::uuid[])
			ORDER BY instrument_id, plugin_id, covered_from
		`, pq.Array(uuids))
	}
	if err != nil {
		return nil, fmt.Errorf("list corporate event coverage: %w", err)
	}
	defer rows.Close()
	var out []db.CorporateEventCoverage
	for rows.Next() {
		var c db.CorporateEventCoverage
		var instUUID uuid.UUID
		if err := rows.Scan(&instUUID, &c.PluginID, &c.CoveredFrom, &c.CoveredTo, &c.FetchedAt); err != nil {
			return nil, fmt.Errorf("list corporate event coverage scan: %w", err)
		}
		c.InstrumentID = instUUID.String()
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateCorporateEventFetchBlock implements db.CorporateEventDB.
func (p *Postgres) CreateCorporateEventFetchBlock(ctx context.Context, instrumentID, pluginID, reason string) error {
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO corporate_event_fetch_blocks (instrument_id, plugin_id, reason)
		VALUES ($1, $2, $3)
		ON CONFLICT (instrument_id, plugin_id)
		DO UPDATE SET reason = EXCLUDED.reason, created_at = now()
	`, instrumentID, pluginID, reason)
	if err != nil {
		return fmt.Errorf("create corporate event fetch block: %w", err)
	}
	return nil
}

// DeleteCorporateEventFetchBlock implements db.CorporateEventDB.
func (p *Postgres) DeleteCorporateEventFetchBlock(ctx context.Context, instrumentID, pluginID string) error {
	_, err := p.q.ExecContext(ctx, `
		DELETE FROM corporate_event_fetch_blocks WHERE instrument_id = $1 AND plugin_id = $2
	`, instrumentID, pluginID)
	if err != nil {
		return fmt.Errorf("delete corporate event fetch block: %w", err)
	}
	return nil
}

// ListCorporateEventFetchBlocks implements db.CorporateEventDB.
func (p *Postgres) ListCorporateEventFetchBlocks(ctx context.Context) ([]db.CorporateEventFetchBlock, error) {
	rows, err := p.q.QueryContext(ctx, `
		SELECT instrument_id, plugin_id, reason, created_at
		FROM corporate_event_fetch_blocks ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list corporate event fetch blocks: %w", err)
	}
	defer rows.Close()
	var out []db.CorporateEventFetchBlock
	for rows.Next() {
		var b db.CorporateEventFetchBlock
		if err := rows.Scan(&b.InstrumentID, &b.PluginID, &b.Reason, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListStockSplitsForExport implements db.CorporateEventDB.
func (p *Postgres) ListStockSplitsForExport(ctx context.Context) ([]db.ExportStockSplit, error) {
	q := `
		SELECT best_id.identifier_type, best_id.value, COALESCE(best_id.domain, '') AS domain,
			COALESCE(i.asset_class, '') AS asset_class,
			s.data_provider, s.ex_date, s.split_from::text, s.split_to::text
		FROM stock_splits s
		JOIN instruments i ON i.id = s.instrument_id
		` + bestIdentifierJoin + `
		ORDER BY best_id.identifier_type, best_id.value, s.ex_date
	`
	rows, err := p.q.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list stock splits for export: %w", err)
	}
	defer rows.Close()
	var out []db.ExportStockSplit
	for rows.Next() {
		var r db.ExportStockSplit
		if err := rows.Scan(&r.IdentifierType, &r.IdentifierValue, &r.IdentifierDomain,
			&r.AssetClass, &r.DataProvider, &r.ExDate, &r.SplitFrom, &r.SplitTo); err != nil {
			return nil, fmt.Errorf("list stock splits for export scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListCashDividendsForExport implements db.CorporateEventDB.
func (p *Postgres) ListCashDividendsForExport(ctx context.Context) ([]db.ExportCashDividend, error) {
	q := `
		SELECT best_id.identifier_type, best_id.value, COALESCE(best_id.domain, '') AS domain,
			COALESCE(i.asset_class, '') AS asset_class,
			d.data_provider, d.ex_date, d.pay_date, d.record_date, d.declaration_date,
			d.amount::text, d.currency, d.frequency, d.type
		FROM cash_dividends d
		JOIN instruments i ON i.id = d.instrument_id
		` + bestIdentifierJoin + `
		ORDER BY best_id.identifier_type, best_id.value, d.ex_date
	`
	rows, err := p.q.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list cash dividends for export: %w", err)
	}
	defer rows.Close()
	var out []db.ExportCashDividend
	for rows.Next() {
		var r db.ExportCashDividend
		var pay, rec, decl sql.NullTime
		var freq sql.NullString
		if err := rows.Scan(&r.IdentifierType, &r.IdentifierValue, &r.IdentifierDomain,
			&r.AssetClass, &r.DataProvider, &r.ExDate, &pay, &rec, &decl,
			&r.Amount, &r.Currency, &freq, &r.Type); err != nil {
			return nil, fmt.Errorf("list cash dividends for export scan: %w", err)
		}
		if pay.Valid {
			t := pay.Time
			r.PayDate = &t
		}
		if rec.Valid {
			t := rec.Time
			r.RecordDate = &t
		}
		if decl.Valid {
			t := decl.Time
			r.DeclarationDate = &t
		}
		if freq.Valid {
			r.Frequency = freq.String
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// HeldEventBearingInstruments implements db.CorporateEventDB.
// Returns one row per instrument that needs corporate event coverage:
//   - Directly held STOCK/ETF instruments (existing behavior)
//   - Underlyings of held OPTION/FUTURE instruments (new)
//
// For underlyings discovered via derivatives, the earliest tx date is the
// minimum across all derivatives on that underlying. This ensures the
// corporate event worker fetches events from the earliest derivative trade.
func (p *Postgres) HeldEventBearingInstruments(ctx context.Context) ([]db.HeldInstrument, error) {
	rows, err := p.q.QueryContext(ctx, `
		WITH direct AS (
			SELECT t.instrument_id, MIN(t.timestamp)::date AS earliest
			FROM txs t
			JOIN instruments i ON i.id = t.instrument_id
			WHERE i.asset_class IN ('STOCK', 'ETF')
			GROUP BY t.instrument_id
		),
		via_derivative AS (
			SELECT i.underlying_id AS instrument_id, MIN(t.timestamp)::date AS earliest
			FROM txs t
			JOIN instruments i ON i.id = t.instrument_id
			WHERE i.asset_class IN ('OPTION', 'FUTURE') AND i.underlying_id IS NOT NULL
			GROUP BY i.underlying_id
		)
		SELECT instrument_id, MIN(earliest) AS earliest
		FROM (SELECT * FROM direct UNION ALL SELECT * FROM via_derivative) combined
		GROUP BY instrument_id
		ORDER BY instrument_id
	`)
	if err != nil {
		return nil, fmt.Errorf("held event-bearing instruments: %w", err)
	}
	defer rows.Close()
	var out []db.HeldInstrument
	for rows.Next() {
		var instUUID uuid.UUID
		var earliest time.Time
		if err := rows.Scan(&instUUID, &earliest); err != nil {
			return nil, fmt.Errorf("held event-bearing instruments scan: %w", err)
		}
		out = append(out, db.HeldInstrument{
			InstrumentID:   instUUID.String(),
			EarliestTxDate: earliest,
		})
	}
	return out, rows.Err()
}

// SplitsByUnderlyingTicker implements db.CorporateEventDB.
func (p *Postgres) SplitsByUnderlyingTicker(ctx context.Context, ticker string) ([]db.StockSplit, error) {
	rows, err := p.q.QueryContext(ctx, `
		SELECT ss.instrument_id, ss.ex_date, ss.split_from, ss.split_to, ss.data_provider, ss.fetched_at
		FROM stock_splits ss
		JOIN instrument_identifiers ii ON ii.instrument_id = ss.instrument_id
		WHERE ii.identifier_type = 'MIC_TICKER' AND ii.value = $1
		ORDER BY ss.ex_date
	`, ticker)
	if err != nil {
		return nil, fmt.Errorf("splits by underlying ticker: %w", err)
	}
	defer rows.Close()
	var out []db.StockSplit
	for rows.Next() {
		var s db.StockSplit
		var instUUID uuid.UUID
		if err := rows.Scan(&instUUID, &s.ExDate, &s.SplitFrom, &s.SplitTo, &s.DataProvider, &s.FetchedAt); err != nil {
			return nil, fmt.Errorf("splits by underlying ticker scan: %w", err)
		}
		s.InstrumentID = instUUID.String()
		out = append(out, s)
	}
	return out, rows.Err()
}

// InstrumentsWithSplits implements db.CorporateEventDB. Returns instruments
// that have splits directly or via their underlying (for derivatives).
func (p *Postgres) InstrumentsWithSplits(ctx context.Context, instrumentIDs []string) ([]string, error) {
	if len(instrumentIDs) == 0 {
		return nil, nil
	}
	uuids := make([]uuid.UUID, 0, len(instrumentIDs))
	for _, id := range instrumentIDs {
		u, err := uuid.Parse(id)
		if err != nil {
			return nil, fmt.Errorf("instruments with splits: invalid id %q: %w", id, err)
		}
		uuids = append(uuids, u)
	}
	rows, err := p.q.QueryContext(ctx, `
		SELECT DISTINCT id FROM (
			SELECT instrument_id AS id FROM stock_splits
			WHERE instrument_id = ANY($1::uuid[])
			UNION
			SELECT i.id FROM instruments i
			JOIN stock_splits s ON s.instrument_id = i.underlying_id
			WHERE i.id = ANY($1::uuid[])
		) t
	`, pq.Array(uuids))
	if err != nil {
		return nil, fmt.Errorf("instruments with splits: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("instruments with splits scan: %w", err)
		}
		out = append(out, id.String())
	}
	return out, rows.Err()
}

// BlockedCorporateEventPluginsForInstruments implements db.CorporateEventDB.
func (p *Postgres) BlockedCorporateEventPluginsForInstruments(ctx context.Context, instrumentIDs []string) (map[string]map[string]bool, error) {
	if len(instrumentIDs) == 0 {
		return nil, nil
	}
	rows, err := p.q.QueryContext(ctx, `
		SELECT instrument_id, plugin_id FROM corporate_event_fetch_blocks
		WHERE instrument_id = ANY($1)
	`, pq.Array(instrumentIDs))
	if err != nil {
		return nil, fmt.Errorf("blocked corporate event plugins for instruments: %w", err)
	}
	defer rows.Close()
	out := make(map[string]map[string]bool)
	for rows.Next() {
		var instID, pluginID string
		if err := rows.Scan(&instID, &pluginID); err != nil {
			return nil, err
		}
		if out[instID] == nil {
			out[instID] = make(map[string]bool)
		}
		out[instID][pluginID] = true
	}
	return out, rows.Err()
}

// ApplyOptionSplit implements db.CorporateEventDB. All mutations run in a
// single transaction: delete old OCC, insert new OCC, update strike,
// recompute split-adjusted tx values, update identified_at. The split_factor_at
// SQL function looks up splits via the underlying_id FK, so no derived split
// row is needed on the option instrument.
func (p *Postgres) ApplyOptionSplit(ctx context.Context, params db.OptionSplitParams) error {
	return p.runInTx(ctx, func(tx queryable) error {
		txp := &Postgres{q: tx}
		if err := txp.DeleteInstrumentIdentifier(ctx, params.InstrumentID, "OCC", params.OldOCCValue); err != nil {
			return fmt.Errorf("apply option split: delete old OCC: %w", err)
		}
		if err := txp.InsertInstrumentIdentifier(ctx, params.InstrumentID, params.NewOCC); err != nil {
			return fmt.Errorf("apply option split: insert new OCC: %w", err)
		}
		if err := txp.UpdateInstrumentStrike(ctx, params.InstrumentID, params.NewStrike); err != nil {
			return fmt.Errorf("apply option split: update strike: %w", err)
		}
		if params.NewName != "" {
			if err := txp.UpdateInstrumentName(ctx, params.InstrumentID, params.NewName); err != nil {
				return fmt.Errorf("apply option split: update name: %w", err)
			}
		}
		if err := txp.RecomputeSplitAdjustments(ctx, params.InstrumentID); err != nil {
			return fmt.Errorf("apply option split: recompute adjustments: %w", err)
		}
		if err := txp.UpdateIdentifiedAt(ctx, params.InstrumentID); err != nil {
			return fmt.Errorf("apply option split: update identified_at: %w", err)
		}
		return nil
	})
}

// RecomputeSplitAdjustments implements db.CorporateEventDB. Two UPDATEs (one
// for eod_prices, one for txs) recompute the split_adjusted_* columns from raw
// values multiplied by the cumulative split factor for splits with ex_date
// strictly after the row's reference date (fetched_at::date for prices,
// timestamp::date for txs). Idempotent: factor is recomputed from scratch
// each call. When instrumentID is empty, every instrument with at least one
// stock_splits row is recomputed in the same transaction.
func (p *Postgres) RecomputeSplitAdjustments(ctx context.Context, instrumentID string) error {
	var (
		instFilter string
		args       []any
	)
	if instrumentID != "" {
		id, err := uuid.Parse(instrumentID)
		if err != nil {
			return fmt.Errorf("recompute split adjustments: invalid instrument id %q: %w", instrumentID, err)
		}
		instFilter = "= $1::uuid"
		args = append(args, id)
	} else {
		instFilter = "IN (SELECT DISTINCT instrument_id FROM stock_splits)"
	}

	return p.runInTx(ctx, func(exec queryable) error {
		// Prices: compute the factor once per (instrument, date) in a
		// FROM-subquery, then reference f.factor in all SET clauses.
		// open/high/low/close are NUMERIC; volume is BIGINT (multiplied).
		priceSQL := fmt.Sprintf(`
			UPDATE eod_prices ep SET
				split_adjusted_open    = CASE WHEN ep.open   IS NULL THEN NULL
					ELSE ep.open   / f.factor::numeric END,
				split_adjusted_high    = CASE WHEN ep.high   IS NULL THEN NULL
					ELSE ep.high   / f.factor::numeric END,
				split_adjusted_low     = CASE WHEN ep.low    IS NULL THEN NULL
					ELSE ep.low    / f.factor::numeric END,
				split_adjusted_close   = ep.close / f.factor::numeric,
				split_adjusted_volume  = CASE WHEN ep.volume IS NULL THEN NULL
					ELSE round(ep.volume::numeric * f.factor::numeric)::bigint END
			FROM (
				SELECT instrument_id, fetched_at,
					split_factor_at(instrument_id, fetched_at::date) AS factor
				FROM eod_prices
				WHERE instrument_id %s
			) f
			WHERE ep.instrument_id = f.instrument_id
			  AND ep.fetched_at = f.fetched_at
		`, instFilter)
		if _, err := exec.ExecContext(ctx, priceSQL, args...); err != nil {
			return fmt.Errorf("recompute split adjustments (prices): %w", err)
		}

		// Txs: factor is already double precision so no cast is needed.
		txSQL := fmt.Sprintf(`
			UPDATE txs t SET
				split_adjusted_quantity   = t.quantity * f.factor,
				split_adjusted_unit_price = CASE WHEN t.unit_price IS NULL THEN NULL
					ELSE t.unit_price / f.factor END
			FROM (
				SELECT id, split_factor_at(instrument_id, timestamp::date) AS factor
				FROM txs
				WHERE instrument_id IS NOT NULL
				  AND instrument_id %s
			) f
			WHERE t.id = f.id
		`, instFilter)
		if _, err := exec.ExecContext(ctx, txSQL, args...); err != nil {
			return fmt.Errorf("recompute split adjustments (txs): %w", err)
		}
		return nil
	})
}

// InsertUnhandledCorporateEvent implements db.CorporateEventDB.
func (p *Postgres) InsertUnhandledCorporateEvent(ctx context.Context, event db.UnhandledCorporateEvent) error {
	var dataJSON []byte
	if event.Data != nil {
		if !json.Valid(event.Data) {
			return fmt.Errorf("insert unhandled corporate event: data is not valid JSON")
		}
		dataJSON = event.Data
	}
	instUUID, err := uuid.Parse(event.InstrumentID)
	if err != nil {
		return fmt.Errorf("insert unhandled corporate event: invalid instrument id: %w", err)
	}
	_, err = p.q.ExecContext(ctx, `
		INSERT INTO unhandled_corporate_events (instrument_id, event_type, ex_date, detail, data)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (instrument_id, event_type, ex_date) WHERE NOT resolved DO NOTHING
	`, instUUID, event.EventType, nullTime(event.ExDate), event.Detail, dataJSON)
	if err != nil {
		return fmt.Errorf("insert unhandled corporate event: %w", err)
	}
	return nil
}

// ListUnhandledCorporateEvents implements db.CorporateEventDB.
func (p *Postgres) ListUnhandledCorporateEvents(ctx context.Context, includeResolved bool, pageSize int32, pageToken string) ([]db.UnhandledCorporateEvent, int32, string, error) {
	offset := decodePageToken(pageToken)

	filter := "WHERE NOT resolved"
	if includeResolved {
		filter = ""
	}

	var total int32
	if err := p.q.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM unhandled_corporate_events %s`, filter)).Scan(&total); err != nil {
		return nil, 0, "", fmt.Errorf("count unhandled corporate events: %w", err)
	}
	if total == 0 {
		return nil, 0, "", nil
	}

	rows, err := p.q.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, instrument_id, event_type, ex_date, detail, data, resolved, created_at
		FROM unhandled_corporate_events %s
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`, filter), pageSize+1, offset)
	if err != nil {
		return nil, 0, "", fmt.Errorf("list unhandled corporate events: %w", err)
	}
	defer rows.Close()

	var out []db.UnhandledCorporateEvent
	for rows.Next() {
		var e db.UnhandledCorporateEvent
		var id, instID uuid.UUID
		var exDate sql.NullTime
		var data []byte
		if err := rows.Scan(&id, &instID, &e.EventType, &exDate, &e.Detail, &data, &e.Resolved, &e.CreatedAt); err != nil {
			return nil, 0, "", fmt.Errorf("scan unhandled corporate event: %w", err)
		}
		e.ID = id.String()
		e.InstrumentID = instID.String()
		if exDate.Valid {
			e.ExDate = &exDate.Time
		}
		e.Data = data
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, "", err
	}

	var nextToken string
	if int32(len(out)) > pageSize {
		out = out[:pageSize]
		nextToken = encodePageToken(offset + int64(pageSize))
	}
	return out, total, nextToken, nil
}

// CountUnhandledCorporateEvents implements db.CorporateEventDB.
func (p *Postgres) CountUnhandledCorporateEvents(ctx context.Context) (int32, error) {
	var count int32
	if err := p.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM unhandled_corporate_events WHERE NOT resolved`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count unhandled corporate events: %w", err)
	}
	return count, nil
}

// ResolveUnhandledCorporateEvent implements db.CorporateEventDB.
func (p *Postgres) ResolveUnhandledCorporateEvent(ctx context.Context, id string) error {
	eventUUID, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("resolve unhandled corporate event: invalid id: %w", err)
	}
	result, err := p.q.ExecContext(ctx, `UPDATE unhandled_corporate_events SET resolved = true WHERE id = $1`, eventUUID)
	if err != nil {
		return fmt.Errorf("resolve unhandled corporate event: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("resolve unhandled corporate event: not found")
	}
	return nil
}

