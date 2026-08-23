package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
// effect, and is picked up by the first run after it does. ex_date <= expiry is
// the scope guard: OCC restates a contract on the effective date, so a split
// only reaches the contracts still listed that day and one that had already
// expired was never restated. valid_from < ex_date is the correctness guard: the
// OCC symbol in force reflects a split only if the name became correct on or
// after the split took effect. Together they make the work list
// self-describing, so the pass needs no record of which cycle a split arrived in
// and re-running it is a no-op.
//
// The guard reads the OCC row's own valid_from rather than a stamp on the
// instrument, which is what lets a split learned of after the fact still select
// the option: a minted name's valid_from is the ex_date it was minted from, a
// market fact, where a knowledge-time stamp would already sit after the newly
// learned split's ex_date and exclude it forever.
//
// A NULL valid_from falls back to the option's own first trade date. A source
// states an option under the name current at its export, and an export cannot
// precede the purchase it describes, so that date is the floor that holds
// without a recorded vintage. With no transactions at all there is no floor and
// the name predates everything, as a NULL stamp used to mean.
//
// The OCC row is read through a LEFT JOIN LATERAL taking one row. LEFT so an
// option carrying no OCC identifier still reaches the pass, which reports it as
// an unhandled event rather than passing over it in silence; LIMIT 1 so an
// option that somehow wears two names at once cannot multiply its splits and be
// restated twice over.
//
// The scope guard is per joined row, not per option, so an option that lived
// through one split and expired before the next comes back pending for the first
// alone and the pass restates only that one.
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
		LEFT JOIN LATERAL (
		    SELECT ii.valid_from
		    FROM instrument_identifiers ii
		    WHERE ii.instrument_id = o.id
		      AND ii.identifier_type = 'OCC'
		      AND ii.valid_before IS NULL
		    ORDER BY ii.valid_from DESC NULLS LAST
		    LIMIT 1
		) occ ON true
		JOIN stock_splits s ON s.instrument_id = o.underlying_id
		WHERE o.asset_class = 'OPTION'
		  AND s.ex_date <= CURRENT_DATE
		  AND s.ex_date <= o.expiry
		  AND COALESCE(
		        occ.valid_from,
		        (SELECT MIN(t.trade_date)::date FROM txs t WHERE t.instrument_id = o.id),
		        '-infinity'::date
		      ) < s.ex_date
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
//
// One statement does the whole job: the caller's rows are unnested, each is left
// joined to the security's line in the currency family it states, the ones that
// found a line are inserted, and the ones that did not are returned. Resolving
// the line here rather than in each caller is what keeps a single rule over
// every writer -- the fetcher, the archive import, and whatever a broker
// statement parser becomes.
//
// The join is on the family rather than the code, so a provider quoting the
// London line's dividend in pence files against the line stored as GBP. The
// amount keeps the code it was quoted in; see the column comment in
// 001_initial.sql.
//
// A currency the security has no line in mints nothing. A dividend says a
// payment was made in a currency, which is not the claim that the security
// trades in it -- a broker converting into the account currency reports exactly
// that. See docs/adr/0073-a-dividend-names-a-line-it-does-not-mint.md.
func (p *Postgres) UpsertCashDividends(ctx context.Context, dividends []db.CashDividend) ([]db.CashDividend, error) {
	if len(dividends) == 0 {
		return nil, nil
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
	rows, err := p.q.QueryContext(ctx, `
		WITH input AS (
			SELECT * FROM unnest($1::uuid[], $2::date[], $3::date[], $4::date[], $5::date[],
				$6::numeric[], $7::text[], $8::text[], $9::text[], $10::text[],
				$11::timestamptz[])
				AS t(instrument_id, ex_date, pay_date, record_date, declaration_date,
					amount, currency, frequency, type, data_provider, first_known_at)
		),
		resolved AS (
			SELECT i.*, l.id AS listing_id
			FROM input i
			LEFT JOIN instrument_listings l
				ON l.instrument_id = i.instrument_id
				AND l.currency IS NOT NULL
				AND currency_family(l.currency) = currency_family(i.currency)
		),
		stored AS (
			INSERT INTO cash_dividends (
				listing_id, ex_date, pay_date, record_date, declaration_date,
				amount, currency, frequency, type, data_provider, first_known_at
			)
			SELECT listing_id, ex_date, pay_date, record_date, declaration_date,
				amount, currency, frequency, type, data_provider,
				COALESCE(first_known_at, now())
			FROM resolved WHERE listing_id IS NOT NULL
			ON CONFLICT (listing_id, ex_date) DO UPDATE SET
				pay_date         = EXCLUDED.pay_date,
				record_date      = EXCLUDED.record_date,
				declaration_date = EXCLUDED.declaration_date,
				amount           = EXCLUDED.amount,
				currency         = EXCLUDED.currency,
				frequency        = EXCLUDED.frequency,
				type             = EXCLUDED.type,
				data_provider    = EXCLUDED.data_provider,
				first_known_at   = LEAST(cash_dividends.first_known_at, EXCLUDED.first_known_at)
		)
		SELECT instrument_id, ex_date, pay_date, record_date, declaration_date,
			amount::text, currency, frequency, type, data_provider, first_known_at
		FROM resolved WHERE listing_id IS NULL
		ORDER BY instrument_id, ex_date
	`, pq.Array(instIDs), pq.Array(exDates),
		pq.Array(payDates), pq.Array(recordDates), pq.Array(declDates),
		pq.Array(amounts), pq.Array(currencies), pq.Array(frequencies),
		pq.Array(types), pq.Array(providers), pq.Array(knownAts))
	if err != nil {
		return nil, fmt.Errorf("upsert cash dividends: %w", err)
	}
	defer rows.Close()
	var unfiled []db.CashDividend
	for rows.Next() {
		var d db.CashDividend
		var instUUID uuid.UUID
		var pay, record, decl, known sql.NullTime
		var freq sql.NullString
		if err := rows.Scan(&instUUID, &d.ExDate, &pay, &record, &decl,
			&d.Amount, &d.Currency, &freq, &d.Type, &d.DataProvider, &known); err != nil {
			return nil, fmt.Errorf("upsert cash dividends scan: %w", err)
		}
		d.InstrumentID = instUUID.String()
		setDividendOptionals(&d, pay, record, decl, freq)
		if known.Valid {
			d.FirstKnownAt = known.Time
		}
		unfiled = append(unfiled, d)
	}
	return unfiled, rows.Err()
}

// setDividendOptionals copies the nullable columns onto a dividend row, which
// the upsert's rejects and the two reads all scan the same way.
func setDividendOptionals(d *db.CashDividend, pay, record, decl sql.NullTime, freq sql.NullString) {
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
}

// ListCashDividends implements db.CorporateEventDB. It reads a security's
// dividends across every one of its lines, which is what an admin looking at a
// security wants; the line each belongs to is on the row.
func (p *Postgres) ListCashDividends(ctx context.Context, instrumentID string) ([]db.CashDividend, error) {
	id, err := uuid.Parse(instrumentID)
	if err != nil {
		return nil, fmt.Errorf("list cash dividends: invalid instrument id %q: %w", instrumentID, err)
	}
	rows, err := p.q.QueryContext(ctx, `
		SELECT l.instrument_id, d.listing_id, d.ex_date, d.pay_date, d.record_date,
			d.declaration_date, d.amount::text, d.currency, d.frequency, d.type,
			d.data_provider, d.first_known_at
		FROM cash_dividends d
		JOIN instrument_listings l ON l.id = d.listing_id
		WHERE l.instrument_id = $1
		ORDER BY d.ex_date, d.listing_id
	`, id)
	if err != nil {
		return nil, fmt.Errorf("list cash dividends: %w", err)
	}
	defer rows.Close()
	var out []db.CashDividend
	for rows.Next() {
		var d db.CashDividend
		var instUUID, listingUUID uuid.UUID
		var pay, record, decl sql.NullTime
		var freq sql.NullString
		if err := rows.Scan(&instUUID, &listingUUID, &d.ExDate, &pay, &record, &decl,
			&d.Amount, &d.Currency, &freq, &d.Type, &d.DataProvider, &d.FirstKnownAt); err != nil {
			return nil, fmt.Errorf("list cash dividends scan: %w", err)
		}
		d.InstrumentID = instUUID.String()
		d.ListingID = listingUUID.String()
		setDividendOptionals(&d, pay, record, decl, freq)
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteCashDividend implements db.CorporateEventDB.
func (p *Postgres) DeleteCashDividend(ctx context.Context, listingID string, exDate time.Time) error {
	_, err := p.q.ExecContext(ctx, `
		DELETE FROM cash_dividends WHERE listing_id = $1 AND ex_date = $2
	`, listingID, exDate)
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
		return upsertCoverageSpan(ctx, exec, corporateEventCoverage, instrumentID, pluginID, from, before, lastFetchedAt)
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
		ORDER BY best_id.identifier_type, best_id.value, COALESCE(best_id.domain, ''), s.ex_date
	`
	rows, err := p.q.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list stock splits for export: %w", err)
	}
	defer rows.Close()
	var out []db.ExportStockSplit
	for rows.Next() {
		var r db.ExportStockSplit
		if err := rows.Scan(&r.Ref.Type, &r.Ref.Value, &r.Ref.Domain,
			&r.AssetClass, &r.DataProvider, &r.ExDate, &r.SplitFrom, &r.SplitTo,
			&r.FirstKnownAt); err != nil {
			return nil, fmt.Errorf("list stock splits for export scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListCashDividendsForExport implements db.CorporateEventDB.
//
// The row is named by the security even though it is stored against a line: an
// archive group is per instrument, and the dividend's own currency field is what
// says which line within it. See docs/spec/archive-format.md#corporate-events.
func (p *Postgres) ListCashDividendsForExport(ctx context.Context) ([]db.ExportCashDividend, error) {
	q := `
		SELECT best_id.identifier_type, best_id.value, COALESCE(best_id.domain, '') AS domain,
			COALESCE(i.asset_class, '') AS asset_class,
			d.data_provider, d.ex_date, d.pay_date, d.record_date, d.declaration_date,
			d.amount::text, d.currency, d.frequency, d.type, d.first_known_at
		FROM cash_dividends d
		JOIN instrument_listings l ON l.id = d.listing_id
		JOIN instruments i ON i.id = l.instrument_id
		` + bestIdentifierJoin + `
		ORDER BY best_id.identifier_type, best_id.value, COALESCE(best_id.domain, ''), d.ex_date, d.currency
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
		if err := rows.Scan(&r.Ref.Type, &r.Ref.Value, &r.Ref.Domain,
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
			SELECT t.instrument_id, MIN(t.order_date)::date AS earliest
			FROM txs t
			JOIN instruments i ON i.id = t.instrument_id
			WHERE i.asset_class IN ('STOCK', 'ETF') AND t.account_type = 'USER'
			GROUP BY t.instrument_id
		),
		via_derivative AS (
			SELECT i.underlying_id AS instrument_id, MIN(t.order_date)::date AS earliest
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

// ApplyOptionSplit implements db.CorporateEventDB. All mutations run in a single
// transaction: close the OCC symbol still in force, mint one row per pending
// split, update the strike, recompute split-adjusted tx values. The
// split_factor_at SQL function looks up splits via the underlying_id FK, so no
// derived split row is needed on the option instrument.
//
// The old symbol is closed rather than deleted. A broker file exported before
// the ex_date names it, and deleting it leaves that file nothing to resolve to,
// so the import invents a duplicate instrument instead. See
// docs/adr/0055-identifier-validity-is-an-interval.md.
//
// The name is not written here. recompute_instrument_name derives it from the
// identifier still in force, which after the mint is the last one.
func (p *Postgres) ApplyOptionSplit(ctx context.Context, params db.OptionSplitParams) error {
	if len(params.Mints) == 0 {
		return fmt.Errorf("apply option split: no mints")
	}
	uid, err := uuid.Parse(params.InstrumentID)
	if err != nil {
		return fmt.Errorf("apply option split: invalid instrument id %q: %w", params.InstrumentID, err)
	}
	from := params.Mints[0].ExDate
	mints := make([]db.IdentifierInput, len(params.Mints))
	for i, m := range params.Mints {
		mints[i] = m.OCC
		mints[i].ValidFrom = &params.Mints[i].ExDate
		if i+1 < len(params.Mints) {
			mints[i].ValidBefore = &params.Mints[i+1].ExDate
		}
	}

	return p.runInTx(ctx, func(tx queryable) error {
		txp := &Postgres{q: tx}
		// Absorb first, before anything is written. A name about to be minted
		// that another instrument already holds is a duplicate of this contract:
		// it was bought again, or its prices fetched, while the split was still
		// unknown, so the post-split symbol resolved to nothing and was given an
		// instrument of its own. Letting the insert fail instead would roll the
		// restatement back and leave the option pending, and every later cycle
		// would collide the same way.
		for _, idn := range mints {
			if err := absorbDuplicateHolder(ctx, tx, uid, idn); err != nil {
				return fmt.Errorf("apply option split: %w", err)
			}
		}
		// This call owns the option's OCC history from the first ex_date
		// onwards, so anything already sitting in that window goes -- including
		// whatever an absorbed duplicate just brought with it, and whatever an
		// earlier overlapping run of the pass minted. The pass is driven by both
		// the fetch cycle and the import job, so a run can plan from a snapshot
		// another has already superseded; without this, the later run would try
		// to close a row starting on the very date it is closing at, which is a
		// zero-length interval the CHECK refuses.
		//
		// Nothing outside the window is caught by it. An OCC row starts either
		// at the ex_date of the split that minted it or at the vintage that
		// supplied it, and every pending split is later than the name in force.
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM instrument_identifiers
			WHERE instrument_id = $1 AND identifier_type = 'OCC' AND valid_from >= $2
		`, uid, from); err != nil {
			return fmt.Errorf("apply option split: clear the restated window: %w", err)
		}
		// Whatever is left in force is the name the contract traded under before
		// the ex_date. Closing rather than deleting it is what lets a file
		// exported before the split still resolve to this contract. Every open
		// row closes, not just the value the caller read, so the write converges
		// whatever snapshot it planned from.
		if _, err := tx.ExecContext(ctx, `
			UPDATE instrument_identifiers SET valid_before = $2
			WHERE instrument_id = $1 AND identifier_type = 'OCC' AND valid_before IS NULL
		`, uid, from); err != nil {
			return fmt.Errorf("apply option split: close OCC identifiers: %w", err)
		}
		for _, idn := range mints {
			if err := insertIdentifierRow(ctx, tx, uid, idn); err != nil {
				return fmt.Errorf("apply option split: mint %s: %w", idn.Ref.Value, err)
			}
		}
		if err := txp.UpdateInstrumentStrike(ctx, params.InstrumentID, params.Mints[len(params.Mints)-1].Strike); err != nil {
			return fmt.Errorf("apply option split: update strike: %w", err)
		}
		if err := txp.RecomputeSplitAdjustments(ctx, params.InstrumentID); err != nil {
			return fmt.Errorf("apply option split: recompute adjustments: %w", err)
		}
		return nil
	})
}

// absorbDuplicateHolder merges away any other instrument holding the name idn is
// about to claim.
//
// The option being restated is the survivor rather than pickSurvivor's answer:
// the caller holds its id and the rest of the transaction runs against it, and
// it is the row carrying the contract's history, where the duplicate holds only
// what arrived after the split.
func absorbDuplicateHolder(ctx context.Context, tx queryable, instrumentID uuid.UUID, idn db.IdentifierInput) error {
	holder, err := identifierHolder(ctx, tx, idn)
	if err != nil {
		return err
	}
	if holder == uuid.Nil || holder == instrumentID {
		return nil
	}
	if err := mergeInstruments(ctx, tx, instrumentID, holder); err != nil {
		return fmt.Errorf("absorb duplicate %s holding %s: %w", holder, idn.Ref.Value, err)
	}
	return nil
}

func insertIdentifierRow(ctx context.Context, tx queryable, instrumentID uuid.UUID, idn db.IdentifierInput) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO instrument_identifiers (instrument_id, identifier_type, domain, value, canonical, valid_from, valid_before)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, instrumentID, idn.Ref.Type, nullStr(idn.Ref.Domain), idn.Ref.Value, idn.Canonical, nullTime(idn.ValidFrom), nullTime(idn.ValidBefore))
	return err
}

// identifierHolder returns the instrument whose row for this name overlaps the
// interval being claimed, or uuid.Nil when none does.
func identifierHolder(ctx context.Context, tx queryable, idn db.IdentifierInput) (uuid.UUID, error) {
	var holder uuid.UUID
	err := tx.QueryRowContext(ctx, `
		SELECT instrument_id FROM instrument_identifiers
		WHERE identifier_type = $1 AND COALESCE(domain, '') = $2 AND value = $3
		  AND daterange(valid_from, valid_before) && daterange($4, $5)
		LIMIT 1
	`, idn.Ref.Type, idn.Ref.Domain, idn.Ref.Value, nullTime(idn.ValidFrom), nullTime(idn.ValidBefore)).Scan(&holder)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("find identifier holder for %s: %w", idn.Ref.Value, err)
	}
	return holder, nil
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
		// Prices: compute the factor once per (listing, date) in a LATERAL, then
		// reference f.num and f.den in all SET clauses. LATERAL rather than a
		// plain subquery selecting (split_factor_at(...)).*, which would call the
		// function once per output column.
		//
		// A split is an action on the security and every line splits with it, so
		// the factor is looked up for the listing's instrument. That is the join
		// the grain split costs here: bars hang off the listing, splits off the
		// security above it.
		//
		// The factor is a rational and divides last: a price adjusts by its
		// reciprocal, so the multiplication is by den and the division by num.
		// open/high/low/close are NUMERIC; volume is BIGINT and adjusts the other
		// way (more shares trade in adjusted-share terms).
		priceSQL := fmt.Sprintf(`
			UPDATE eod_prices ep SET
				split_adjusted_open    = CASE WHEN ep.open   IS NULL THEN NULL
					ELSE ep.open   * f.den / f.num END,
				split_adjusted_high    = CASE WHEN ep.high   IS NULL THEN NULL
					ELSE ep.high   * f.den / f.num END,
				split_adjusted_low     = CASE WHEN ep.low    IS NULL THEN NULL
					ELSE ep.low    * f.den / f.num END,
				split_adjusted_close   = ep.close * f.den / f.num,
				split_adjusted_volume  = CASE WHEN ep.volume IS NULL THEN NULL
					ELSE round(ep.volume::numeric * f.num / f.den)::bigint END
			FROM (
				SELECT p.listing_id, p.price_date, f.num, f.den
				FROM eod_prices p
				JOIN instrument_listings l ON l.id = p.listing_id,
					LATERAL split_factor_at(l.instrument_id, p.share_count_basis) f
				WHERE l.instrument_id %s
			) f
			WHERE ep.listing_id = f.listing_id
			  AND ep.price_date = f.price_date
		`, instFilter)
		if _, err := exec.ExecContext(ctx, priceSQL, args...); err != nil {
			return fmt.Errorf("recompute split adjustments (prices): %w", err)
		}

		// Txs: a quantity adjusts by the factor and a price by its reciprocal, so
		// the two invert each other and the cost-basis invariant
		// quantity * unit_price == split_adjusted_quantity * split_adjusted_unit_price
		// holds. Both multiply before the single division; the result rounds to
		// the columns' declared scale, which is where a reverse /3 lands.
		txSQL := fmt.Sprintf(`
			UPDATE txs t SET
				split_adjusted_quantity   = t.quantity * f.num / f.den,
				split_adjusted_unit_price = CASE WHEN t.unit_price IS NULL THEN NULL
					ELSE t.unit_price * f.den / f.num END
			FROM (
				SELECT x.id, f.num, f.den
				FROM txs x,
					LATERAL split_factor_at(x.instrument_id, x.share_count_basis) f
				WHERE x.instrument_id IS NOT NULL
				  AND x.instrument_id %s
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

// exportCoverageRow is a sqlx-scannable version of db.ExportCoverageRow.
type exportCoverageRow struct {
	db.InstrumentRef
	AssetClass string    `db:"asset_class"`
	From       time.Time `db:"covered_from"`
	Before     time.Time `db:"covered_before"`
}

func toExportCoverageRows(rows []exportCoverageRow) []db.ExportCoverageRow {
	out := make([]db.ExportCoverageRow, len(rows))
	for i, r := range rows {
		out[i] = db.ExportCoverageRow{
			Ref:        r.InstrumentRef,
			AssetClass: r.AssetClass,
			From:       r.From,
			Before:     r.Before,
		}
	}
	return out
}

// ListCorporateEventCoverageForExport implements db.CorporateEventDB.
func (p *Postgres) ListCorporateEventCoverageForExport(ctx context.Context) ([]db.ExportCoverageRow, error) {
	q := `
		SELECT best_id.identifier_type, best_id.value, COALESCE(best_id.domain, '') AS domain,
			COALESCE(i.asset_class, '') AS asset_class,
			mc.covered_from, mc.covered_before
		FROM merged_corporate_event_coverage mc
		JOIN instruments i ON i.id = mc.instrument_id
		` + bestIdentifierJoin + `
		ORDER BY best_id.identifier_type, best_id.value, COALESCE(best_id.domain, ''), covered_from
	`
	var rows []exportCoverageRow
	if err := p.q.SelectContext(ctx, &rows, q); err != nil {
		return nil, fmt.Errorf("list corporate event coverage for export: %w", err)
	}
	return toExportCoverageRows(rows), nil
}

// ListCorporateEventFetchBlocksForExport implements db.CorporateEventDB.
func (p *Postgres) ListCorporateEventFetchBlocksForExport(ctx context.Context) ([]db.ExportFetchBlock, error) {
	return listFetchBlocksForExport(ctx, p, corporateEventFetchBlocks)
}

// UpsertCorporateEventFetchBlocks implements db.CorporateEventDB.
func (p *Postgres) UpsertCorporateEventFetchBlocks(ctx context.Context, blocks []db.FetchBlockInput) error {
	return upsertFetchBlocks(ctx, p, corporateEventFetchBlocks, blocks)
}

// ListUnhandledCorporateEventsForExport implements db.CorporateEventDB.
func (p *Postgres) ListUnhandledCorporateEventsForExport(ctx context.Context) ([]db.ExportUnhandledCorporateEvent, error) {
	q := `
		SELECT best_id.identifier_type, best_id.value, COALESCE(best_id.domain, '') AS domain,
			u.event_type, u.ex_date, u.detail, u.data, u.resolved, u.created_at
		FROM unhandled_corporate_events u
		JOIN instruments i ON i.id = u.instrument_id
		` + bestIdentifierJoin + `
		ORDER BY best_id.identifier_type, best_id.value, COALESCE(best_id.domain, ''),
			u.created_at, u.event_type
	`
	rows, err := p.q.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list unhandled corporate events for export: %w", err)
	}
	defer rows.Close()

	var out []db.ExportUnhandledCorporateEvent
	for rows.Next() {
		var r db.ExportUnhandledCorporateEvent
		var exDate sql.NullTime
		var data []byte
		if err := rows.Scan(&r.Ref.Type, &r.Ref.Value, &r.Ref.Domain,
			&r.EventType, &exDate, &r.Detail, &data, &r.Resolved, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan unhandled corporate event for export: %w", err)
		}
		if exDate.Valid {
			d := exDate.Time
			r.ExDate = &d
		}
		r.Data = data
		out = append(out, r)
	}
	return out, rows.Err()
}

// RestoreUnhandledCorporateEvents implements db.CorporateEventDB.
//
// The dedup index is partial -- it covers unresolved rows only -- so it cannot
// back an ON CONFLICT for a resolved row. The guard is a NOT EXISTS on the
// natural key together with the resolved flag, which makes re-importing a file
// a no-op without asserting a uniqueness the table does not have. A stored
// resolved row and an incoming unresolved one stay distinct, which is what
// already happens when a refetch re-detects an event an admin has judged.
func (p *Postgres) RestoreUnhandledCorporateEvents(ctx context.Context, events []db.UnhandledCorporateEvent) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	const q = `
		INSERT INTO unhandled_corporate_events (instrument_id, event_type, ex_date, detail, data, resolved, created_at)
		SELECT $1, $2, $3, $4, $5, $6, $7
		WHERE NOT EXISTS (
			SELECT 1 FROM unhandled_corporate_events e
			WHERE e.instrument_id = $1 AND e.event_type = $2
				AND e.ex_date IS NOT DISTINCT FROM $3::date
				AND e.resolved = $6
		)`
	var inserted int
	err := p.runInTx(ctx, func(exec queryable) error {
		for _, e := range events {
			instUUID, err := uuid.Parse(e.InstrumentID)
			if err != nil {
				return fmt.Errorf("restore unhandled corporate event: invalid instrument id: %w", err)
			}
			var dataJSON []byte
			if e.Data != nil {
				if !json.Valid(e.Data) {
					return fmt.Errorf("restore unhandled corporate event: data is not valid JSON")
				}
				dataJSON = e.Data
			}
			res, err := exec.ExecContext(ctx, q, instUUID, e.EventType, nullTime(e.ExDate),
				e.Detail, dataJSON, e.Resolved, e.CreatedAt)
			if err != nil {
				return fmt.Errorf("restore unhandled corporate event: %w", err)
			}
			n, _ := res.RowsAffected()
			inserted += int(n)
		}
		return nil
	})
	return inserted, err
}
