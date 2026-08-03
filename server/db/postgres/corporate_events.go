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
	knownAts := make([]*time.Time, len(splits))
	for i, s := range splits {
		instIDs[i] = s.InstrumentID
		exDates[i] = s.ExDate
		froms[i] = s.SplitFrom
		tos[i] = s.SplitTo
		providers[i] = s.DataProvider
		if !s.FirstKnownAt.IsZero() {
			t := s.FirstKnownAt
			knownAts[i] = &t
		}
	}
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO stock_splits (instrument_id, ex_date, split_from, split_to, data_provider, first_known_at)
		SELECT instrument_id, ex_date, split_from, split_to, data_provider,
			COALESCE(first_known_at, now())
		FROM unnest($1::uuid[], $2::date[], $3::numeric[], $4::numeric[],
			$5::text[], $6::timestamptz[])
			AS t(instrument_id, ex_date, split_from, split_to, data_provider, first_known_at)
		ON CONFLICT (instrument_id, ex_date) DO UPDATE SET
			split_from     = EXCLUDED.split_from,
			split_to       = EXCLUDED.split_to,
			data_provider  = EXCLUDED.data_provider,
			first_known_at = LEAST(stock_splits.first_known_at, EXCLUDED.first_known_at)
	`, pq.Array(instIDs), pq.Array(exDates), pq.Array(froms), pq.Array(tos), pq.Array(providers),
		pq.Array(knownAts))
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
		SELECT instrument_id, ex_date, split_from::text, split_to::text, data_provider, first_known_at
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
		if err := rows.Scan(&instUUID, &s.ExDate, &s.SplitFrom, &s.SplitTo, &s.DataProvider, &s.FirstKnownAt); err != nil {
			return nil, fmt.Errorf("list stock splits scan: %w", err)
		}
		s.InstrumentID = instUUID.String()
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListPendingOptionSplits implements db.CorporateEventDB.
//
// The predicate is the whole of the pass's state. ex_date <= CURRENT_DATE is the
// future-date guard: a split fetched by the lookahead sits inert until it takes
// effect, and is picked up by the first run after it does. identity_as_of <
// ex_date is the correctness guard: the stored OCC symbol reflects a split only
// if it was derived on or after the split took effect, and a NULL predates every
// split. Together they make the work list self-describing, so the pass needs no
// record of which cycle a split arrived in and re-running it is a no-op.
func (p *Postgres) ListPendingOptionSplits(ctx context.Context, underlyingID string) ([]db.PendingOptionSplits, error) {
	var (
		filter string
		args   []any
	)
	if underlyingID != "" {
		id, err := uuid.Parse(underlyingID)
		if err != nil {
			return nil, fmt.Errorf("list pending option splits: invalid underlying id %q: %w", underlyingID, err)
		}
		filter = "AND o.underlying_id = $1"
		args = append(args, id)
	}
	rows, err := p.q.QueryContext(ctx, fmt.Sprintf(`
		SELECT o.id, s.instrument_id, s.ex_date, s.split_from::text, s.split_to::text,
		       s.data_provider, s.first_known_at
		FROM instruments o
		JOIN stock_splits s ON s.instrument_id = o.underlying_id
		WHERE o.asset_class = 'OPTION'
		  AND s.ex_date <= CURRENT_DATE
		  AND (o.identity_as_of IS NULL OR o.identity_as_of < s.ex_date)
		  %s
		ORDER BY o.id, s.ex_date
	`, filter), args...)
	if err != nil {
		return nil, fmt.Errorf("list pending option splits: %w", err)
	}
	defer rows.Close()

	splitsByOption := make(map[uuid.UUID][]db.StockSplit)
	var order []uuid.UUID
	for rows.Next() {
		var optID, underlying uuid.UUID
		var s db.StockSplit
		if err := rows.Scan(&optID, &underlying, &s.ExDate, &s.SplitFrom, &s.SplitTo, &s.DataProvider, &s.FirstKnownAt); err != nil {
			return nil, fmt.Errorf("list pending option splits scan: %w", err)
		}
		s.InstrumentID = underlying.String()
		if _, seen := splitsByOption[optID]; !seen {
			order = append(order, optID)
		}
		splitsByOption[optID] = append(splitsByOption[optID], s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(order) == 0 {
		return nil, nil
	}

	// Hydrate the option rows separately: the pass needs their identifiers to
	// find the OCC symbol, and ListInstrumentsByIDs already loads those.
	ids := make([]string, len(order))
	for i, id := range order {
		ids[i] = id.String()
	}
	instRows, err := p.ListInstrumentsByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list pending option splits: load options: %w", err)
	}
	byID := make(map[string]*db.InstrumentRow, len(instRows))
	for _, r := range instRows {
		byID[r.ID] = r
	}

	out := make([]db.PendingOptionSplits, 0, len(order))
	for _, id := range order {
		opt, ok := byID[id.String()]
		if !ok {
			continue
		}
		out = append(out, db.PendingOptionSplits{Option: opt, Splits: splitsByOption[id]})
	}
	return out, nil
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
	knownAts := make([]*time.Time, len(dividends))
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
		if !d.FirstKnownAt.IsZero() {
			t := d.FirstKnownAt
			knownAts[i] = &t
		}
	}
	_, err := p.q.ExecContext(ctx, `
		INSERT INTO cash_dividends (
			instrument_id, ex_date, pay_date, record_date, declaration_date,
			amount, currency, frequency, type, data_provider, first_known_at
		)
		SELECT instrument_id, ex_date, pay_date, record_date, declaration_date,
			amount, currency, frequency, type, data_provider,
			COALESCE(first_known_at, now())
		FROM unnest($1::uuid[], $2::date[], $3::date[], $4::date[], $5::date[],
			$6::numeric[], $7::text[], $8::text[], $9::text[], $10::text[],
			$11::timestamptz[])
			AS t(instrument_id, ex_date, pay_date, record_date, declaration_date,
				amount, currency, frequency, type, data_provider, first_known_at)
		ON CONFLICT (instrument_id, ex_date) DO UPDATE SET
			pay_date         = EXCLUDED.pay_date,
			record_date      = EXCLUDED.record_date,
			declaration_date = EXCLUDED.declaration_date,
			amount           = EXCLUDED.amount,
			currency         = EXCLUDED.currency,
			frequency        = EXCLUDED.frequency,
			type             = EXCLUDED.type,
			data_provider    = EXCLUDED.data_provider,
			first_known_at   = LEAST(cash_dividends.first_known_at, EXCLUDED.first_known_at)
	`, pq.Array(instIDs), pq.Array(exDates),
		pq.Array(payDates), pq.Array(recordDates), pq.Array(declDates),
		pq.Array(amounts), pq.Array(currencies), pq.Array(frequencies),
		pq.Array(types), pq.Array(providers), pq.Array(knownAts))
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
			amount::text, currency, frequency, type, data_provider, first_known_at
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
			&d.Amount, &d.Currency, &freq, &d.Type, &d.DataProvider, &d.FirstKnownAt); err != nil {
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

// UpsertCorporateEventCoverage implements db.CorporateEventDB. The merge runs in
// a single transaction so concurrent inserts cannot leave partial state. See
// upsertCoverageSpan for the merge semantics, which price_coverage shares.
func (p *Postgres) UpsertCorporateEventCoverage(ctx context.Context, instrumentID, pluginID string, from, before time.Time, lastFetchedAt *time.Time) error {
	return p.runInTx(ctx, func(exec queryable) error {
		return upsertCoverageSpan(ctx, exec, corporateEventCoverageTable, instrumentID, pluginID, from, before, lastFetchedAt)
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
			SELECT instrument_id, plugin_id, covered_from, covered_before, last_fetched_at
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
			SELECT instrument_id, plugin_id, covered_from, covered_before, last_fetched_at
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
		if err := rows.Scan(&instUUID, &c.PluginID, &c.CoveredFrom, &c.CoveredBefore, &c.LastFetchedAt); err != nil {
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
		DO UPDATE SET reason = EXCLUDED.reason
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
		SELECT instrument_id, plugin_id, reason, first_blocked_at
		FROM corporate_event_fetch_blocks ORDER BY first_blocked_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list corporate event fetch blocks: %w", err)
	}
	defer rows.Close()
	var out []db.CorporateEventFetchBlock
	for rows.Next() {
		var b db.CorporateEventFetchBlock
		if err := rows.Scan(&b.InstrumentID, &b.PluginID, &b.Reason, &b.FirstBlockedAt); err != nil {
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
			s.data_provider, s.ex_date, s.split_from::text, s.split_to::text,
			s.first_known_at
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
			&r.AssetClass, &r.DataProvider, &r.ExDate, &r.SplitFrom, &r.SplitTo,
			&r.FirstKnownAt); err != nil {
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
			d.amount::text, d.currency, d.frequency, d.type, d.first_known_at
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
			&r.Amount, &r.Currency, &freq, &r.Type, &r.FirstKnownAt); err != nil {
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
			WHERE i.asset_class IN ('STOCK', 'ETF') AND t.account_type = 'USER'
			GROUP BY t.instrument_id
		),
		via_derivative AS (
			SELECT i.underlying_id AS instrument_id, MIN(t.timestamp)::date AS earliest
			FROM txs t
			JOIN instruments i ON i.id = t.instrument_id
			WHERE i.asset_class IN ('OPTION', 'FUTURE') AND i.underlying_id IS NOT NULL
			  AND t.account_type = 'USER'
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
		SELECT ss.instrument_id, ss.ex_date, ss.split_from, ss.split_to, ss.data_provider, ss.first_known_at
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
		if err := rows.Scan(&instUUID, &s.ExDate, &s.SplitFrom, &s.SplitTo, &s.DataProvider, &s.FirstKnownAt); err != nil {
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
// single transaction: replace the OCC identifier, update strike,
// recompute split-adjusted tx values, advance identity_as_of. The split_factor_at
// SQL function looks up splits via the underlying_id FK, so no derived split
// row is needed on the option instrument.
func (p *Postgres) ApplyOptionSplit(ctx context.Context, params db.OptionSplitParams) error {
	return p.runInTx(ctx, func(tx queryable) error {
		txp := &Postgres{q: tx}
		// Every OCC identifier goes, not just the value the caller read. An
		// option has exactly one OCC, and deleting by value leaves a stale one
		// behind when two runs of the pass overlap: the loser's delete matches
		// nothing, its insert of a different adjusted symbol succeeds, and the
		// option ends up resolving under both. Deleting by type makes the
		// operation converge whatever the caller last saw.
		if err := txp.DeleteInstrumentIdentifiersByType(ctx, params.InstrumentID, "OCC"); err != nil {
			return fmt.Errorf("apply option split: delete OCC identifiers: %w", err)
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
		if err := txp.UpdateIdentityAsOf(ctx, params.InstrumentID); err != nil {
			return fmt.Errorf("apply option split: update identity_as_of: %w", err)
		}
		return nil
	})
}

// RecomputeSplitAdjustments implements db.CorporateEventDB. Two UPDATEs (one
// for eod_prices, one for txs) recompute the split_adjusted_* columns from raw
// values multiplied by the cumulative split factor for splits with ex_date
// strictly after the row's share_count_basis. Idempotent: factor is recomputed
// from scratch each call. When instrumentID is empty, every instrument with at least one
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
				SELECT instrument_id, price_date,
					split_factor_at(instrument_id, share_count_basis) AS factor
				FROM eod_prices
				WHERE instrument_id %s
			) f
			WHERE ep.instrument_id = f.instrument_id
			  AND ep.price_date = f.price_date
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
				SELECT id, split_factor_at(instrument_id, share_count_basis) AS factor
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

// ListCorporateEventCoverageForExport implements db.CorporateEventDB.
func (p *Postgres) ListCorporateEventCoverageForExport(ctx context.Context) ([]db.ExportCoverageRow, error) {
	q := `
		SELECT best_id.identifier_type, best_id.value, COALESCE(best_id.domain, '') AS domain,
			lower(sub.r) AS covered_from, upper(sub.r) AS covered_before
		FROM (
			SELECT instrument_id,
				unnest(range_agg(daterange(covered_from, covered_before))) AS r
			FROM corporate_event_coverage
			GROUP BY instrument_id
		) sub
		JOIN instruments i ON i.id = sub.instrument_id
		` + bestIdentifierJoin + `
		ORDER BY best_id.identifier_type, best_id.value, covered_from
	`
	var rows []exportCoverageRow
	if err := p.q.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("list corporate event coverage for export: %w", err)
	}
	return toExportCoverageRows(rows), nil
}
