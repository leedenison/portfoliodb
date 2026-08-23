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
//
// A posting on a known line contributes to that line alone. One that named none
// contributes to every priceable line of its security instead, because the
// history has to be there for whichever line it turns out to be on -- and it
// costs requests for a line nobody holds, not correctness, since the bars land on
// the line they were quoted for. A listing with no currency is never a target: it
// is not priceable, and a price with no stated currency asserts nothing.
//
// This is the opposite trade from valuation, which reports a holding on no line
// unpriced rather than picking one. Fetching too much is recoverable; valuing at
// a currency nobody stated is not. See
// docs/adr/0072-a-posting-names-a-security-and-a-line.md.
func (p *Postgres) HeldRanges(ctx context.Context, opts db.HeldRangesOpts) ([]db.ListingDateRanges, error) {
	rows, err := p.q.QueryContext(ctx, `
		WITH daily_net AS (
			SELECT l.id AS listing_id, t.order_date::date AS tx_date, SUM(t.quantity) AS day_qty
			FROM txs t
			JOIN instrument_listings l
				ON l.instrument_id = t.instrument_id
				AND (t.listing_id IS NULL OR l.id = t.listing_id)
			WHERE t.instrument_id IS NOT NULL AND t.account_type = 'USER'
			GROUP BY l.id, t.order_date::date
		)
		SELECT listing_id, tx_date,
			SUM(day_qty) OVER (PARTITION BY listing_id ORDER BY tx_date) AS eod_pos
		FROM daily_net
		ORDER BY listing_id, tx_date
	`)
	if err != nil {
		return nil, fmt.Errorf("held ranges query: %w", err)
	}
	defer rows.Close()

	today := time.Now().UTC().Truncate(db.Day)

	var result []db.ListingDateRanges
	var curListing uuid.UUID
	var ranges []db.DateRange
	var rangeStart time.Time
	inRange := false

	flush := func() {
		if len(ranges) == 0 {
			return
		}
		result = append(result, db.ListingDateRanges{
			ListingID: curListing.String(),
			Ranges:    ranges,
		})
	}

	for rows.Next() {
		var listingID uuid.UUID
		var txDate time.Time
		var eodPos decimal.Decimal
		if err := rows.Scan(&listingID, &txDate, &eodPos); err != nil {
			return nil, fmt.Errorf("held ranges scan: %w", err)
		}

		if listingID != curListing {
			// Close open range for previous listing.
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
			curListing = listingID
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

// coverageFilter turns listing IDs into a query argument, or nil for all.
func coverageFilter(listingIDs []string) (interface{}, error) {
	var uuids []uuid.UUID
	for _, id := range listingIDs {
		u, err := uuid.Parse(id)
		if err != nil {
			return nil, fmt.Errorf("invalid listing id %q: %w", id, err)
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
func (p *Postgres) PriceCoverage(ctx context.Context, listingIDs []string) ([]db.ListingDateRanges, error) {
	filter, err := coverageFilter(listingIDs)
	if err != nil {
		return nil, fmt.Errorf("price coverage: %w", err)
	}

	rows, err := p.q.QueryContext(ctx, `
		SELECT listing_id, covered_from AS range_from, covered_before AS range_before
		FROM merged_price_coverage
		WHERE ($1::uuid[] IS NULL OR listing_id = ANY($1))
		ORDER BY listing_id, range_from
	`, filter)
	if err != nil {
		return nil, fmt.Errorf("price coverage query: %w", err)
	}
	defer rows.Close()

	byListing := make(map[string]*db.ListingDateRanges)
	var order []string
	for rows.Next() {
		var listingID uuid.UUID
		var from, before time.Time
		if err := rows.Scan(&listingID, &from, &before); err != nil {
			return nil, fmt.Errorf("price coverage scan: %w", err)
		}
		id := listingID.String()
		entry, ok := byListing[id]
		if !ok {
			entry = &db.ListingDateRanges{ListingID: id}
			byListing[id] = entry
			order = append(order, id)
		}
		entry.Ranges = append(entry.Ranges, db.DateRange{From: from, Before: before})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("price coverage rows: %w", err)
	}

	result := make([]db.ListingDateRanges, len(order))
	for i, id := range order {
		result[i] = *byListing[id]
	}
	return result, nil
}

// PriceCoverageByPlugin implements db.PriceCacheDB.
//
// Keeping the plugin distinction is what lets a range one plugin declined still
// be offered to another, and lets a newly configured plugin be asked about
// history the existing ones could not reach.
func (p *Postgres) PriceCoverageByPlugin(ctx context.Context, listingIDs []string) (map[string]map[string][]db.DateRange, error) {
	filter, err := coverageFilter(listingIDs)
	if err != nil {
		return nil, fmt.Errorf("price coverage by plugin: %w", err)
	}

	rows, err := p.q.QueryContext(ctx, `
		SELECT listing_id, plugin_id, covered_from, covered_before
		FROM price_coverage
		WHERE ($1::uuid[] IS NULL OR listing_id = ANY($1))
		ORDER BY listing_id, plugin_id, covered_from
	`, filter)
	if err != nil {
		return nil, fmt.Errorf("price coverage by plugin query: %w", err)
	}
	defer rows.Close()

	result := make(map[string]map[string][]db.DateRange)
	for rows.Next() {
		var listingID uuid.UUID
		var pluginID string
		var from, before time.Time
		if err := rows.Scan(&listingID, &pluginID, &from, &before); err != nil {
			return nil, fmt.Errorf("price coverage by plugin scan: %w", err)
		}
		id := listingID.String()
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
// It computes held ranges minus price coverage per listing using SubtractRanges.
func (p *Postgres) PriceGaps(ctx context.Context, opts db.HeldRangesOpts) ([]db.ListingDateRanges, error) {
	held, err := p.HeldRanges(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("price gaps: held ranges: %w", err)
	}
	if len(held) == 0 {
		return nil, nil
	}

	// Collect listing IDs for coverage lookup.
	ids := make([]string, len(held))
	for i, h := range held {
		ids[i] = h.ListingID
	}

	coverage, err := p.PriceCoverage(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("price gaps: coverage: %w", err)
	}

	// Index coverage by listing ID.
	coverageByListing := make(map[string][]db.DateRange, len(coverage))
	for _, c := range coverage {
		coverageByListing[c.ListingID] = c.Ranges
	}

	var result []db.ListingDateRanges
	for _, h := range held {
		gaps := db.SubtractRanges(h.Ranges, coverageByListing[h.ListingID])
		if len(gaps) > 0 {
			result = append(result, db.ListingDateRanges{
				ListingID: h.ListingID,
				Ranges:    gaps,
			})
		}
	}
	return result, nil
}

// FXGaps implements db.PriceCacheDB.
// It computes date ranges where FX rates are needed but not yet cached.
// Two sources of demand:
//  1. Held listings in a currency other than USD need that currency's FX pair.
//  2. Active display currencies (from users table) need their FX pair for any
//     date where listings not in that currency are held.
//
// An FX pair is itself a security with one listing, quoted in USD under the
// pivot in docs/adr/0006-fx-as-synthetic-instruments.md, and the gaps are keyed
// on that listing like any other.
func (p *Postgres) FXGaps(ctx context.Context, opts db.HeldRangesOpts) ([]db.ListingDateRanges, error) {
	held, err := p.HeldRanges(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("fx gaps: held ranges: %w", err)
	}
	if len(held) == 0 {
		return nil, nil
	}

	// Collect held listing IDs.
	heldIDs := make([]string, len(held))
	for i, h := range held {
		heldIDs[i] = h.ListingID
	}

	// Batch query: for each held listing, get its currency and the listing of the
	// corresponding FX pair (if any). FX_PAIR names the security, so the pair's
	// own line is reached through it.
	rows, err := p.q.QueryContext(ctx, `
		SELECT
			l.id::text AS held_id,
			l.currency,
			fx_pl.listing_id::text AS fx_listing_id
		FROM instrument_listings l
		INNER JOIN instrument_identifiers fx_ii
			ON fx_ii.identifier_type = 'FX_PAIR'
			AND fx_ii.value = l.currency || 'USD'
			AND fx_ii.valid_before IS NULL
		INNER JOIN instrument_priced_listing fx_pl
			ON fx_pl.instrument_id = fx_ii.instrument_id
		WHERE l.id = ANY($1::uuid[])
			AND l.currency != 'USD'
	`, pq.Array(heldIDs))
	if err != nil {
		return nil, fmt.Errorf("fx gaps: currency lookup: %w", err)
	}
	defer rows.Close()

	// Map held listing ID -> FX pair listing ID, and held listing ID -> currency.
	heldToFX := make(map[string]string)
	heldToCurrency := make(map[string]string)
	for rows.Next() {
		var heldID, currency, fxListingID string
		if err := rows.Scan(&heldID, &currency, &fxListingID); err != nil {
			return nil, fmt.Errorf("fx gaps: scan: %w", err)
		}
		heldToFX[heldID] = fxListingID
		heldToCurrency[heldID] = currency
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("fx gaps: rows: %w", err)
	}

	// Build needed ranges per FX pair listing by merging held ranges.
	// Source 1: held listings in a currency other than USD.
	fxNeeded := make(map[string][]db.DateRange)
	for _, h := range held {
		fxID, ok := heldToFX[h.ListingID]
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

	// Merge overlapping ranges and collect FX listing IDs.
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
	coverageByListing := make(map[string][]db.DateRange, len(coverage))
	for _, c := range coverage {
		coverageByListing[c.ListingID] = c.Ranges
	}

	// Subtract coverage from needed ranges.
	var result []db.ListingDateRanges
	for _, fxID := range fxIDs {
		gaps := db.SubtractRanges(fxNeeded[fxID], coverageByListing[fxID])
		if len(gaps) > 0 {
			result = append(result, db.ListingDateRanges{
				ListingID: fxID,
				Ranges:    gaps,
			})
		}
	}
	return result, nil
}

// addDisplayCurrencyNeeds adds FX rate demand for active non-USD display currencies.
// For each display currency D, the D/USD rate is needed on any date where a
// listing whose currency is not D is held.
func (p *Postgres) addDisplayCurrencyNeeds(
	ctx context.Context,
	held []db.ListingDateRanges,
	heldToCurrency map[string]string, // only non-USD listings; USD and unknown are absent
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

	// Look up the FX pair listing for each display currency.
	fxRows, err := p.q.QueryContext(ctx, `
		SELECT ii.value, pl.listing_id::text
		FROM instrument_identifiers ii
		JOIN instrument_priced_listing pl ON pl.instrument_id = ii.instrument_id
		WHERE ii.identifier_type = 'FX_PAIR' AND ii.value = ANY($1) AND ii.valid_before IS NULL
	`, pq.Array(displayCurrencyFXValues(displayCurrencies)))
	if err != nil {
		return fmt.Errorf("fx gaps: display fx lookup: %w", err)
	}
	defer fxRows.Close()

	// Map "DUSD" -> FX pair listing ID.
	dcFXMap := make(map[string]string)
	for fxRows.Next() {
		var val, fxListingID string
		if err := fxRows.Scan(&val, &fxListingID); err != nil {
			return fmt.Errorf("fx gaps: scan display fx: %w", err)
		}
		dcFXMap[val] = fxListingID
	}
	if err := fxRows.Err(); err != nil {
		return fmt.Errorf("fx gaps: display fx rows: %w", err)
	}

	for _, dc := range displayCurrencies {
		fxListingID, ok := dcFXMap[dc+"USD"]
		if !ok {
			continue // no FX pair listing for this currency
		}

		// Collect held ranges for listings whose currency != dc. A listing
		// quoted in USD (absent from heldToCurrency) needs the display rate too,
		// not being in dc either.
		for _, h := range held {
			lstCurrency, isNonUSD := heldToCurrency[h.ListingID]
			if isNonUSD && lstCurrency == dc {
				continue // listing already in the display currency
			}
			fxNeeded[fxListingID] = append(fxNeeded[fxListingID], h.Ranges...)
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

// dedupeByListingDate keeps the last bar supplied for each (listing, date).
func dedupeByListingDate(prices []db.EODPrice) []db.EODPrice {
	type key struct {
		listingID string
		date      time.Time
	}
	seen := make(map[key]int, len(prices))
	out := make([]db.EODPrice, 0, len(prices))
	for _, pr := range prices {
		k := key{pr.ListingID, pr.PriceDate.Truncate(db.Day)}
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
// dates, per (listing, provider). Single days merge into runs, so a dense
// series costs one span rather than one per bar.
func coverSuppliedDates(ctx context.Context, exec queryable, prices []db.EODPrice) error {
	type key struct{ listingID, provider string }
	byKey := make(map[key][]db.DateRange)
	for _, pr := range prices {
		d := pr.PriceDate
		k := key{pr.ListingID, pr.DataProvider}
		byKey[k] = append(byKey[k], db.DateRange{From: d, Before: d.Add(db.Day)})
	}
	for k, ranges := range byKey {
		for _, r := range db.MergeRanges(ranges) {
			if err := upsertCoverageSpan(ctx, exec, priceCoverage, k.listingID, k.provider, r.From, r.Before, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func upsertPrices(ctx context.Context, exec queryable, prices []db.EODPrice) error {
	// ON CONFLICT DO UPDATE cannot touch the same row twice in one statement, and
	// providers do repeat a date within a response. Last one supplied wins.
	prices = dedupeByListingDate(prices)

	listingIDs := make([]string, len(prices))
	dates := make([]time.Time, len(prices))
	opens := make([]*decimal.Decimal, len(prices))
	highs := make([]*decimal.Decimal, len(prices))
	lows := make([]*decimal.Decimal, len(prices))
	closes := make([]decimal.Decimal, len(prices))
	volumes := make([]*int64, len(prices))
	providers := make([]string, len(prices))
	fetchedAts := make([]time.Time, len(prices))
	bases := make([]time.Time, len(prices))
	adjCloses := make([]*decimal.Decimal, len(prices))
	now := time.Now()

	for i, pr := range prices {
		listingIDs[i] = pr.ListingID
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
		// Undeclared basis means as-traded: denominated in the share count
		// current on the bar's own date.
		if pr.ShareCountBasis != nil {
			bases[i] = *pr.ShareCountBasis
		} else {
			bases[i] = pr.PriceDate
		}
	}

	_, err := exec.ExecContext(ctx, `
		INSERT INTO eod_prices (listing_id, price_date, open, high, low, close, volume, data_provider, last_fetched_at, share_count_basis, adjusted_close)
		SELECT unnest($1::uuid[]), unnest($2::date[]), unnest($3::numeric[]),
			unnest($4::numeric[]), unnest($5::numeric[]),
			unnest($6::numeric[]), unnest($7::bigint[]),
			unnest($8::text[]),
			unnest($9::timestamptz[]), unnest($10::date[]),
			unnest($11::numeric[])
		ON CONFLICT (listing_id, price_date) DO UPDATE SET
			open = EXCLUDED.open,
			high = EXCLUDED.high,
			low = EXCLUDED.low,
			close = EXCLUDED.close,
			volume = EXCLUDED.volume,
			data_provider = EXCLUDED.data_provider,
			last_fetched_at = EXCLUDED.last_fetched_at,
			-- Basis travels with the raw values it describes; restating one
			-- without the other would leave the row self-inconsistent.
			share_count_basis = EXCLUDED.share_count_basis,
			adjusted_close = EXCLUDED.adjusted_close
	`, pq.Array(listingIDs), pq.Array(dates), pq.Array(opens),
		pq.Array(highs), pq.Array(lows), pq.Array(closes),
		pq.Array(volumes), pq.Array(providers),
		pq.Array(fetchedAts), pq.Array(bases), pq.Array(adjCloses))
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
func (p *Postgres) UpsertPricesForRange(ctx context.Context, listingID, provider string, bars []db.EODPrice, from, before time.Time, fetchedAt *time.Time) error {
	return p.runInTx(ctx, func(exec queryable) error {
		if len(bars) > 0 {
			priced := make([]db.EODPrice, len(bars))
			for i, b := range bars {
				b.ListingID = listingID
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
		return upsertCoverageSpan(ctx, exec, priceCoverage, listingID, provider, from, before, fetchedAt)
	})
}
