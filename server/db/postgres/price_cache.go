package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"

	"github.com/leedenison/portfoliodb/server/db"
)

// HeldRanges implements db.PriceCacheDB.
func (p *Postgres) HeldRanges(ctx context.Context, opts db.HeldRangesOpts) ([]db.InstrumentDateRanges, error) {
	rows, err := p.q.QueryContext(ctx, `
		WITH daily_net AS (
			SELECT instrument_id, order_date::date AS tx_date, SUM(quantity) AS day_qty
			FROM txs
			WHERE instrument_id IS NOT NULL AND account_type = 'USER'
			GROUP BY instrument_id, order_date::date
		)
		SELECT instrument_id, tx_date,
			SUM(day_qty) OVER (PARTITION BY instrument_id ORDER BY tx_date) AS eod_pos
		FROM daily_net
		ORDER BY instrument_id, tx_date
	`)
	if err != nil {
		return nil, fmt.Errorf("held ranges query: %w", err)
	}
	defer rows.Close()

	today := time.Now().UTC().Truncate(db.Day)

	var result []db.InstrumentDateRanges
	var curInst uuid.UUID
	var ranges []db.DateRange
	var rangeStart time.Time
	inRange := false

	flush := func() {
		if len(ranges) == 0 {
			return
		}
		result = append(result, db.InstrumentDateRanges{
			InstrumentID: curInst.String(),
			Ranges:       ranges,
		})
	}

	for rows.Next() {
		var instID uuid.UUID
		var txDate time.Time
		var eodPos decimal.Decimal
		if err := rows.Scan(&instID, &txDate, &eodPos); err != nil {
			return nil, fmt.Errorf("held ranges scan: %w", err)
		}

		if instID != curInst {
			// Close open range for previous instrument.
			if inRange {
				before := today.Add(db.Day)
				if !opts.ExtendToToday {
					// No extend: we don't know when position ended, use last tx date + 1.
					before = rangeStart.Add(db.Day)
				}
				ranges = append(ranges, db.DateRange{From: rangeStart, Before: before})
				inRange = false
			}
			flush()
			curInst = instID
			ranges = nil
		}

		if !eodPos.IsZero() && !inRange {
			rangeStart = txDate
			inRange = true
		} else if eodPos.IsZero() && inRange {
			ranges = append(ranges, db.DateRange{From: rangeStart, Before: txDate})
			inRange = false
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("held ranges rows: %w", err)
	}

	// Close final open range.
	if inRange {
		before := today.Add(db.Day)
		if !opts.ExtendToToday {
			before = rangeStart.Add(db.Day)
		}
		ranges = append(ranges, db.DateRange{From: rangeStart, Before: before})
	}
	flush()

	return result, nil
}

// coverageFilter turns instrument IDs into a query argument, or nil for all.
func coverageFilter(instrumentIDs []string) (interface{}, error) {
	var uuids []uuid.UUID
	for _, id := range instrumentIDs {
		u, err := uuid.Parse(id)
		if err != nil {
			return nil, fmt.Errorf("invalid instrument id %q: %w", id, err)
		}
		uuids = append(uuids, u)
	}
	if len(uuids) == 0 {
		return nil, nil
	}
	return pq.Array(uuids), nil
}

// PriceCoverage implements db.PriceCacheDB.
//
// Coverage is read from price_coverage, not inferred from which dates happen to
// have rows: a span a provider answered with no bars at all is coverage, and
// row presence cannot say so. Spans are merged across plugins, since for "has
// anyone answered for this range" it does not matter who did.
func (p *Postgres) PriceCoverage(ctx context.Context, instrumentIDs []string) ([]db.InstrumentDateRanges, error) {
	filter, err := coverageFilter(instrumentIDs)
	if err != nil {
		return nil, fmt.Errorf("price coverage: %w", err)
	}

	rows, err := p.q.QueryContext(ctx, `
		SELECT instrument_id, covered_from AS range_from, covered_before AS range_before
		FROM merged_price_coverage
		WHERE ($1::uuid[] IS NULL OR instrument_id = ANY($1))
		ORDER BY instrument_id, range_from
	`, filter)
	if err != nil {
		return nil, fmt.Errorf("price coverage query: %w", err)
	}
	defer rows.Close()

	byInst := make(map[string]*db.InstrumentDateRanges)
	var order []string
	for rows.Next() {
		var instID uuid.UUID
		var from, before time.Time
		if err := rows.Scan(&instID, &from, &before); err != nil {
			return nil, fmt.Errorf("price coverage scan: %w", err)
		}
		id := instID.String()
		entry, ok := byInst[id]
		if !ok {
			entry = &db.InstrumentDateRanges{InstrumentID: id}
			byInst[id] = entry
			order = append(order, id)
		}
		entry.Ranges = append(entry.Ranges, db.DateRange{From: from, Before: before})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("price coverage rows: %w", err)
	}

	result := make([]db.InstrumentDateRanges, len(order))
	for i, id := range order {
		result[i] = *byInst[id]
	}
	return result, nil
}

// PriceCoverageByPlugin implements db.PriceCacheDB.
//
// Keeping the plugin distinction is what lets a range one plugin declined still
// be offered to another, and lets a newly configured plugin be asked about
// history the existing ones could not reach.
func (p *Postgres) PriceCoverageByPlugin(ctx context.Context, instrumentIDs []string) (map[string]map[string][]db.DateRange, error) {
	filter, err := coverageFilter(instrumentIDs)
	if err != nil {
		return nil, fmt.Errorf("price coverage by plugin: %w", err)
	}

	rows, err := p.q.QueryContext(ctx, `
		SELECT instrument_id, plugin_id, covered_from, covered_before
		FROM price_coverage
		WHERE ($1::uuid[] IS NULL OR instrument_id = ANY($1))
		ORDER BY instrument_id, plugin_id, covered_from
	`, filter)
	if err != nil {
		return nil, fmt.Errorf("price coverage by plugin query: %w", err)
	}
	defer rows.Close()

	result := make(map[string]map[string][]db.DateRange)
	for rows.Next() {
		var instID uuid.UUID
		var pluginID string
		var from, before time.Time
		if err := rows.Scan(&instID, &pluginID, &from, &before); err != nil {
			return nil, fmt.Errorf("price coverage by plugin scan: %w", err)
		}
		id := instID.String()
		if result[id] == nil {
			result[id] = make(map[string][]db.DateRange)
		}
		result[id][pluginID] = append(result[id][pluginID], db.DateRange{From: from, Before: before})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("price coverage by plugin rows: %w", err)
	}
	return result, nil
}

// PriceGaps implements db.PriceCacheDB.
// It computes held ranges minus price coverage per instrument using SubtractRanges.
func (p *Postgres) PriceGaps(ctx context.Context, opts db.HeldRangesOpts) ([]db.InstrumentDateRanges, error) {
	held, err := p.HeldRanges(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("price gaps: held ranges: %w", err)
	}
	if len(held) == 0 {
		return nil, nil
	}

	// Collect instrument IDs for coverage lookup.
	ids := make([]string, len(held))
	for i, h := range held {
		ids[i] = h.InstrumentID
	}

	coverage, err := p.PriceCoverage(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("price gaps: coverage: %w", err)
	}

	// Index coverage by instrument ID.
	coverageByInst := make(map[string][]db.DateRange, len(coverage))
	for _, c := range coverage {
		coverageByInst[c.InstrumentID] = c.Ranges
	}

	var result []db.InstrumentDateRanges
	for _, h := range held {
		gaps := db.SubtractRanges(h.Ranges, coverageByInst[h.InstrumentID])
		if len(gaps) > 0 {
			result = append(result, db.InstrumentDateRanges{
				InstrumentID: h.InstrumentID,
				Ranges:       gaps,
			})
		}
	}
	return result, nil
}

// FXGaps implements db.PriceCacheDB.
// It computes date ranges where FX rates are needed but not yet cached.
// Two sources of demand:
//  1. Held instruments with non-USD currencies need their currency's FX pair.
//  2. Active display currencies (from users table) need their FX pair for any
//     date where instruments not in that currency are held.
func (p *Postgres) FXGaps(ctx context.Context, opts db.HeldRangesOpts) ([]db.InstrumentDateRanges, error) {
	held, err := p.HeldRanges(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("fx gaps: held ranges: %w", err)
	}
	if len(held) == 0 {
		return nil, nil
	}

	// Collect held instrument IDs.
	heldIDs := make([]string, len(held))
	for i, h := range held {
		heldIDs[i] = h.InstrumentID
	}

	// Batch query: for each held instrument, get its currency and the
	// corresponding FX pair instrument ID (if any).
	rows, err := p.q.QueryContext(ctx, `
		SELECT
			i.id::text AS held_id,
			i.currency,
			fx_ii.instrument_id::text AS fx_instrument_id
		FROM instruments i
		INNER JOIN instrument_identifiers fx_ii
			ON fx_ii.identifier_type = 'FX_PAIR'
			AND fx_ii.value = i.currency || 'USD'
		WHERE i.id = ANY($1::uuid[])
			AND i.currency IS NOT NULL
			AND i.currency != 'USD'
	`, pq.Array(heldIDs))
	if err != nil {
		return nil, fmt.Errorf("fx gaps: currency lookup: %w", err)
	}
	defer rows.Close()

	// Map held instrument ID -> FX pair instrument ID, and instrument ID -> currency.
	heldToFX := make(map[string]string)
	heldToCurrency := make(map[string]string)
	for rows.Next() {
		var heldID, currency, fxInstID string
		if err := rows.Scan(&heldID, &currency, &fxInstID); err != nil {
			return nil, fmt.Errorf("fx gaps: scan: %w", err)
		}
		heldToFX[heldID] = fxInstID
		heldToCurrency[heldID] = currency
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("fx gaps: rows: %w", err)
	}

	// Build needed ranges per FX pair instrument by merging held ranges.
	// Source 1: held instruments with non-USD currencies.
	fxNeeded := make(map[string][]db.DateRange)
	for _, h := range held {
		fxID, ok := heldToFX[h.InstrumentID]
		if !ok {
			continue
		}
		fxNeeded[fxID] = append(fxNeeded[fxID], h.Ranges...)
	}

	// Source 2: active display currencies.
	// For each non-USD display currency D, we need D/USD rates for dates where
	// any instrument with currency != D is held.
	if err := p.addDisplayCurrencyNeeds(ctx, held, heldToCurrency, fxNeeded); err != nil {
		return nil, err
	}

	if len(fxNeeded) == 0 {
		return nil, nil
	}

	// Merge overlapping ranges and collect FX instrument IDs.
	var fxIDs []string
	for fxID, ranges := range fxNeeded {
		fxNeeded[fxID] = db.MergeRanges(ranges)
		fxIDs = append(fxIDs, fxID)
	}

	// Get existing FX rate coverage.
	coverage, err := p.PriceCoverage(ctx, fxIDs)
	if err != nil {
		return nil, fmt.Errorf("fx gaps: coverage: %w", err)
	}
	coverageByInst := make(map[string][]db.DateRange, len(coverage))
	for _, c := range coverage {
		coverageByInst[c.InstrumentID] = c.Ranges
	}

	// Subtract coverage from needed ranges.
	var result []db.InstrumentDateRanges
	for _, fxID := range fxIDs {
		gaps := db.SubtractRanges(fxNeeded[fxID], coverageByInst[fxID])
		if len(gaps) > 0 {
			result = append(result, db.InstrumentDateRanges{
				InstrumentID: fxID,
				Ranges:       gaps,
			})
		}
	}
	return result, nil
}

// addDisplayCurrencyNeeds adds FX rate demand for active non-USD display currencies.
// For each display currency D, the D/USD rate is needed on any date where an
// instrument with currency != D is held.
func (p *Postgres) addDisplayCurrencyNeeds(
	ctx context.Context,
	held []db.InstrumentDateRanges,
	heldToCurrency map[string]string, // only non-USD instruments; USD and NULL are absent
	fxNeeded map[string][]db.DateRange,
) error {
	// Query distinct non-USD display currencies.
	dcRows, err := p.q.QueryContext(ctx, `
		SELECT DISTINCT display_currency FROM users WHERE display_currency != 'USD'
	`)
	if err != nil {
		return fmt.Errorf("fx gaps: display currencies: %w", err)
	}
	defer dcRows.Close()

	var displayCurrencies []string
	for dcRows.Next() {
		var dc string
		if err := dcRows.Scan(&dc); err != nil {
			return fmt.Errorf("fx gaps: scan display currency: %w", err)
		}
		displayCurrencies = append(displayCurrencies, dc)
	}
	if err := dcRows.Err(); err != nil {
		return fmt.Errorf("fx gaps: display currency rows: %w", err)
	}
	if len(displayCurrencies) == 0 {
		return nil
	}

	// Look up FX pair instrument IDs for each display currency.
	fxRows, err := p.q.QueryContext(ctx, `
		SELECT value, instrument_id::text
		FROM instrument_identifiers
		WHERE identifier_type = 'FX_PAIR' AND value = ANY($1)
	`, pq.Array(displayCurrencyFXValues(displayCurrencies)))
	if err != nil {
		return fmt.Errorf("fx gaps: display fx lookup: %w", err)
	}
	defer fxRows.Close()

	// Map "DUSD" -> FX instrument ID.
	dcFXMap := make(map[string]string)
	for fxRows.Next() {
		var val, fxInstID string
		if err := fxRows.Scan(&val, &fxInstID); err != nil {
			return fmt.Errorf("fx gaps: scan display fx: %w", err)
		}
		dcFXMap[val] = fxInstID
	}
	if err := fxRows.Err(); err != nil {
		return fmt.Errorf("fx gaps: display fx rows: %w", err)
	}

	for _, dc := range displayCurrencies {
		fxInstID, ok := dcFXMap[dc+"USD"]
		if !ok {
			continue // no FX pair instrument for this currency
		}

		// Collect held ranges for instruments whose currency != dc.
		// Instruments with NULL/USD currency (not in heldToCurrency) also need
		// the display rate since they're not in dc either.
		for _, h := range held {
			instCurrency, isNonUSD := heldToCurrency[h.InstrumentID]
			if isNonUSD && instCurrency == dc {
				continue // instrument already in display currency
			}
			fxNeeded[fxInstID] = append(fxNeeded[fxInstID], h.Ranges...)
		}
	}
	return nil
}

// displayCurrencyFXValues returns ["EURUSD", "GBPUSD", ...] for lookup.
func displayCurrencyFXValues(currencies []string) []string {
	out := make([]string, len(currencies))
	for i, c := range currencies {
		out[i] = c + "USD"
	}
	return out
}

// UpsertPrices implements db.PriceCacheDB.
// It bulk inserts EOD prices using unnest arrays, updating on conflict.
//
// Each supplied bar covers its own date and nothing more: a caller with no
// range to declare is asserting the days it names, not the span between them.
// Coverage is written in the same transaction as the rows, so no path exists
// that stores a price without recording what it covers.
func (p *Postgres) UpsertPrices(ctx context.Context, prices []db.EODPrice) error {
	if len(prices) == 0 {
		return nil
	}
	return p.runInTx(ctx, func(exec queryable) error {
		if err := upsertPrices(ctx, exec, prices); err != nil {
			return err
		}
		return coverSuppliedDates(ctx, exec, prices)
	})
}

// dedupeByInstrumentDate keeps the last bar supplied for each (instrument, date).
func dedupeByInstrumentDate(prices []db.EODPrice) []db.EODPrice {
	type key struct {
		instrumentID string
		date         time.Time
	}
	seen := make(map[key]int, len(prices))
	out := make([]db.EODPrice, 0, len(prices))
	for _, pr := range prices {
		k := key{pr.InstrumentID, pr.PriceDate.Truncate(db.Day)}
		if i, ok := seen[k]; ok {
			out[i] = pr
			continue
		}
		seen[k] = len(out)
		out = append(out, pr)
	}
	return out
}

// coverSuppliedDates records one coverage span per contiguous run of supplied
// dates, per (instrument, provider). Single days merge into runs, so a dense
// series costs one span rather than one per bar.
func coverSuppliedDates(ctx context.Context, exec queryable, prices []db.EODPrice) error {
	type key struct{ instrumentID, provider string }
	byKey := make(map[key][]db.DateRange)
	for _, pr := range prices {
		d := pr.PriceDate
		k := key{pr.InstrumentID, pr.DataProvider}
		byKey[k] = append(byKey[k], db.DateRange{From: d, Before: d.Add(db.Day)})
	}
	for k, ranges := range byKey {
		for _, r := range db.MergeRanges(ranges) {
			if err := upsertCoverageSpan(ctx, exec, priceCoverageTable, k.instrumentID, k.provider, r.From, r.Before, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func upsertPrices(ctx context.Context, exec queryable, prices []db.EODPrice) error {
	// ON CONFLICT DO UPDATE cannot touch the same row twice in one statement, and
	// providers do repeat a date within a response. Last one supplied wins.
	prices = dedupeByInstrumentDate(prices)

	instIDs := make([]string, len(prices))
	dates := make([]time.Time, len(prices))
	opens := make([]*decimal.Decimal, len(prices))
	highs := make([]*decimal.Decimal, len(prices))
	lows := make([]*decimal.Decimal, len(prices))
	closes := make([]decimal.Decimal, len(prices))
	volumes := make([]*int64, len(prices))
	providers := make([]string, len(prices))
	fetchedAts := make([]time.Time, len(prices))
	adjCloses := make([]*decimal.Decimal, len(prices))
	now := time.Now()

	for i, pr := range prices {
		instIDs[i] = pr.InstrumentID
		dates[i] = pr.PriceDate
		opens[i] = pr.Open
		highs[i] = pr.High
		lows[i] = pr.Low
		closes[i] = pr.Close
		volumes[i] = pr.Volume
		providers[i] = pr.DataProvider
		adjCloses[i] = pr.AdjustedClose
		if pr.LastFetchedAt != nil {
			fetchedAts[i] = *pr.LastFetchedAt
		} else {
			fetchedAts[i] = now
		}
	}

	_, err := exec.ExecContext(ctx, `
		INSERT INTO eod_prices (instrument_id, price_date, open, high, low, close, volume, data_provider, last_fetched_at, adjusted_close)
		SELECT unnest($1::uuid[]), unnest($2::date[]), unnest($3::numeric[]),
			unnest($4::numeric[]), unnest($5::numeric[]),
			unnest($6::numeric[]), unnest($7::bigint[]),
			unnest($8::text[]),
			unnest($9::timestamptz[]), unnest($10::numeric[])
		ON CONFLICT (instrument_id, price_date) DO UPDATE SET
			open = EXCLUDED.open,
			high = EXCLUDED.high,
			low = EXCLUDED.low,
			close = EXCLUDED.close,
			volume = EXCLUDED.volume,
			data_provider = EXCLUDED.data_provider,
			last_fetched_at = EXCLUDED.last_fetched_at,
			adjusted_close = EXCLUDED.adjusted_close
	`, pq.Array(instIDs), pq.Array(dates), pq.Array(opens),
		pq.Array(highs), pq.Array(lows), pq.Array(closes),
		pq.Array(volumes), pq.Array(providers),
		pq.Array(fetchedAts), pq.Array(adjCloses))
	if err != nil {
		return fmt.Errorf("upsert prices: %w", err)
	}
	return nil
}

// UpsertPricesForRange implements db.PriceCacheDB.
//
// The bars are stored as supplied and [from, before) is recorded as coverage, in
// one transaction. Days inside the range with no bar are left absent rather than
// filled: the carry-forward happens at read time, bounded by this same coverage,
// so storing it as well would only be a second copy of a derivable fact.
//
// Coverage is recorded whether or not any bars came back. A provider that
// answered "nothing here" has covered the range just as authoritatively as one
// that returned a full series.
func (p *Postgres) UpsertPricesForRange(ctx context.Context, instrumentID, provider string, bars []db.EODPrice, from, before time.Time, fetchedAt *time.Time) error {
	return p.runInTx(ctx, func(exec queryable) error {
		if len(bars) > 0 {
			priced := make([]db.EODPrice, len(bars))
			for i, b := range bars {
				b.InstrumentID = instrumentID
				b.DataProvider = provider
				if b.LastFetchedAt == nil {
					b.LastFetchedAt = fetchedAt
				}
				priced[i] = b
			}
			if err := upsertPrices(ctx, exec, priced); err != nil {
				return err
			}
		}
		return upsertCoverageSpan(ctx, exec, priceCoverageTable, instrumentID, provider, from, before, fetchedAt)
	})
}
