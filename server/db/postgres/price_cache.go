package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/leedenison/portfoliodb/server/db"
)

// HeldRanges implements db.PriceCacheDB.
func (p *Postgres) HeldRanges(ctx context.Context, opts db.HeldRangesOpts) ([]db.InstrumentDateRanges, error) {
	rows, err := p.q.QueryContext(ctx, `
		WITH daily_net AS (
			SELECT instrument_id, timestamp::date AS tx_date, SUM(quantity) AS day_qty
			FROM txs
			WHERE instrument_id IS NOT NULL
			GROUP BY instrument_id, timestamp::date
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
		if opts.LookbackDays > 0 {
			for i := range ranges {
				ranges[i].From = ranges[i].From.AddDate(0, 0, -opts.LookbackDays)
			}
			ranges = db.MergeRanges(ranges)
		}
		result = append(result, db.InstrumentDateRanges{
			InstrumentID: curInst.String(),
			Ranges:       ranges,
		})
	}

	for rows.Next() {
		var instID uuid.UUID
		var txDate time.Time
		var eodPos float64
		if err := rows.Scan(&instID, &txDate, &eodPos); err != nil {
			return nil, fmt.Errorf("held ranges scan: %w", err)
		}

		if instID != curInst {
			// Close open range for previous instrument.
			if inRange {
				to := today.Add(db.Day)
				if !opts.ExtendToToday {
					// No extend: we don't know when position ended, use last tx date + 1.
					to = rangeStart.Add(db.Day)
				}
				ranges = append(ranges, db.DateRange{From: rangeStart, To: to})
				inRange = false
			}
			flush()
			curInst = instID
			ranges = nil
		}

		if eodPos != 0 && !inRange {
			rangeStart = txDate
			inRange = true
		} else if eodPos == 0 && inRange {
			ranges = append(ranges, db.DateRange{From: rangeStart, To: txDate})
			inRange = false
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("held ranges rows: %w", err)
	}

	// Close final open range.
	if inRange {
		to := today.Add(db.Day)
		if !opts.ExtendToToday {
			to = rangeStart.Add(db.Day)
		}
		ranges = append(ranges, db.DateRange{From: rangeStart, To: to})
	}
	flush()

	return result, nil
}

// PriceCoverage implements db.PriceCacheDB.
func (p *Postgres) PriceCoverage(ctx context.Context, instrumentIDs []string) ([]db.InstrumentDateRanges, error) {
	var uuids []uuid.UUID
	for _, id := range instrumentIDs {
		u, err := uuid.Parse(id)
		if err != nil {
			return nil, fmt.Errorf("price coverage: invalid instrument id %q: %w", id, err)
		}
		uuids = append(uuids, u)
	}

	var filter interface{}
	if len(uuids) > 0 {
		filter = pq.Array(uuids)
	}

	rows, err := p.q.QueryContext(ctx, `
		SELECT instrument_id, lower(r) AS range_from, upper(r) AS range_to
		FROM (
			SELECT instrument_id,
				unnest(range_agg(daterange(price_date, price_date + 1))) AS r
			FROM eod_prices
			WHERE ($1::uuid[] IS NULL OR instrument_id = ANY($1))
			GROUP BY instrument_id
		) sub
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
		var from, to time.Time
		if err := rows.Scan(&instID, &from, &to); err != nil {
			return nil, fmt.Errorf("price coverage scan: %w", err)
		}
		id := instID.String()
		entry, ok := byInst[id]
		if !ok {
			entry = &db.InstrumentDateRanges{InstrumentID: id}
			byInst[id] = entry
			order = append(order, id)
		}
		entry.Ranges = append(entry.Ranges, db.DateRange{From: from, To: to})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("price coverage rows: %w", err)
	}

	result := make([]db.InstrumentDateRanges, len(order))
	for i, id := range order {
		entry := *byInst[id]
		// No bridging needed: the price fetcher worker writes synthetic
		// (LOCF) rows for non-trading days, so coverage is contiguous.
		result[i] = entry
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
// Real prices (synthetic=false) always overwrite existing rows. Synthetic
// prices only overwrite when the existing row is also synthetic.
func (p *Postgres) UpsertPrices(ctx context.Context, prices []db.EODPrice) error {
	if len(prices) == 0 {
		return nil
	}

	instIDs := make([]string, len(prices))
	dates := make([]time.Time, len(prices))
	opens := make([]*float64, len(prices))
	highs := make([]*float64, len(prices))
	lows := make([]*float64, len(prices))
	closes := make([]float64, len(prices))
	volumes := make([]*int64, len(prices))
	providers := make([]string, len(prices))
	synthetics := make([]bool, len(prices))

	for i, pr := range prices {
		instIDs[i] = pr.InstrumentID
		dates[i] = pr.PriceDate
		opens[i] = pr.Open
		highs[i] = pr.High
		lows[i] = pr.Low
		closes[i] = pr.Close
		volumes[i] = pr.Volume
		providers[i] = pr.DataProvider
		synthetics[i] = pr.Synthetic
	}

	_, err := p.q.ExecContext(ctx, `
		INSERT INTO eod_prices (instrument_id, price_date, open, high, low, close, volume, data_provider, synthetic, fetched_at)
		SELECT unnest($1::uuid[]), unnest($2::date[]), unnest($3::double precision[]),
			unnest($4::double precision[]), unnest($5::double precision[]),
			unnest($6::double precision[]), unnest($7::bigint[]),
			unnest($8::text[]), unnest($9::boolean[]), now()
		ON CONFLICT (instrument_id, price_date) DO UPDATE SET
			open = EXCLUDED.open,
			high = EXCLUDED.high,
			low = EXCLUDED.low,
			close = EXCLUDED.close,
			volume = EXCLUDED.volume,
			data_provider = EXCLUDED.data_provider,
			synthetic = EXCLUDED.synthetic,
			fetched_at = EXCLUDED.fetched_at
		WHERE eod_prices.synthetic = true OR EXCLUDED.synthetic = false
	`, pq.Array(instIDs), pq.Array(dates), pq.Array(opens),
		pq.Array(highs), pq.Array(lows), pq.Array(closes),
		pq.Array(volumes), pq.Array(providers), pq.Array(synthetics))
	if err != nil {
		return fmt.Errorf("upsert prices: %w", err)
	}
	return nil
}

// UpsertPricesWithFill implements db.PriceCacheDB.
// It inserts real bars and generates synthetic LOCF prices for every date in
// [from, to) that has no real bar, all in a single SQL round-trip. The last
// non-synthetic close price before `from` seeds the forward-fill for dates
// preceding the first real bar.
func (p *Postgres) UpsertPricesWithFill(ctx context.Context, instrumentID, provider string, bars []db.EODPrice, from, to time.Time) error {
	id, err := uuid.Parse(instrumentID)
	if err != nil {
		return fmt.Errorf("upsert prices with fill: invalid id %q: %w", instrumentID, err)
	}

	dates := make([]time.Time, len(bars))
	opens := make([]*float64, len(bars))
	highs := make([]*float64, len(bars))
	lows := make([]*float64, len(bars))
	closes := make([]float64, len(bars))
	volumes := make([]*int64, len(bars))
	for i, b := range bars {
		dates[i] = b.PriceDate
		opens[i] = b.Open
		highs[i] = b.High
		lows[i] = b.Low
		closes[i] = b.Close
		volumes[i] = b.Volume
	}

	_, err = p.q.ExecContext(ctx, `
		WITH
		seed AS (
			SELECT close FROM eod_prices
			WHERE instrument_id = $1 AND price_date < $2::date AND NOT synthetic
			ORDER BY price_date DESC LIMIT 1
		),
		new_bars AS (
			SELECT unnest($4::date[]) AS price_date,
				unnest($5::double precision[]) AS bopen,
				unnest($6::double precision[]) AS bhigh,
				unnest($7::double precision[]) AS blow,
				unnest($8::double precision[]) AS bclose,
				unnest($9::bigint[]) AS bvolume
		),
		all_points AS (
			-- Virtual seed point before range start for LOCF initialization.
			SELECT ($2::date - 1) AS price_date,
				NULL::double precision AS bopen, NULL::double precision AS bhigh,
				NULL::double precision AS blow, s.close AS bclose,
				NULL::bigint AS bvolume
			FROM seed s
			UNION ALL
			-- Every date in [from, to) with real bar if available.
			SELECT d::date, nb.bopen, nb.bhigh, nb.blow, nb.bclose, nb.bvolume
			FROM generate_series($2::date, $3::date - interval '1 day', '1 day') d
			LEFT JOIN new_bars nb ON nb.price_date = d::date
		),
		grouped AS (
			SELECT *,
				COUNT(bclose) OVER (ORDER BY price_date) AS grp
			FROM all_points
		),
		locf AS (
			SELECT price_date,
				bopen AS open, bhigh AS high, blow AS low,
				FIRST_VALUE(bclose) OVER (PARTITION BY grp ORDER BY price_date) AS close,
				bvolume AS volume,
				(bclose IS NULL) AS synthetic
			FROM grouped
		)
		INSERT INTO eod_prices (instrument_id, price_date, open, high, low, close, volume, data_provider, synthetic, fetched_at)
		SELECT $1::uuid, price_date, open, high, low, close, volume, $10::text, synthetic, now()
		FROM locf
		WHERE price_date >= $2::date AND close IS NOT NULL
		ON CONFLICT (instrument_id, price_date) DO UPDATE SET
			open = EXCLUDED.open, high = EXCLUDED.high, low = EXCLUDED.low,
			close = EXCLUDED.close, volume = EXCLUDED.volume,
			data_provider = EXCLUDED.data_provider, synthetic = EXCLUDED.synthetic,
			fetched_at = EXCLUDED.fetched_at
		WHERE eod_prices.synthetic = true OR EXCLUDED.synthetic = false
	`, id, from, to, pq.Array(dates), pq.Array(opens), pq.Array(highs),
		pq.Array(lows), pq.Array(closes), pq.Array(volumes), provider)
	if err != nil {
		return fmt.Errorf("upsert prices with fill: %w", err)
	}
	return nil
}
