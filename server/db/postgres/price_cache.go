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
		result[i] = *byInst[id]
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
// Stub: will be implemented in a subsequent PR.
func (p *Postgres) FXGaps(ctx context.Context, opts db.HeldRangesOpts) ([]db.InstrumentDateRanges, error) {
	return nil, nil
}

// UpsertPrices implements db.PriceCacheDB.
// It bulk inserts EOD prices using unnest arrays, updating on conflict.
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

	for i, pr := range prices {
		instIDs[i] = pr.InstrumentID
		dates[i] = pr.PriceDate
		opens[i] = pr.Open
		highs[i] = pr.High
		lows[i] = pr.Low
		closes[i] = pr.Close
		volumes[i] = pr.Volume
		providers[i] = pr.DataProvider
	}

	_, err := p.q.ExecContext(ctx, `
		INSERT INTO eod_prices (instrument_id, price_date, open, high, low, close, volume, data_provider, fetched_at)
		SELECT unnest($1::uuid[]), unnest($2::date[]), unnest($3::double precision[]),
			unnest($4::double precision[]), unnest($5::double precision[]),
			unnest($6::double precision[]), unnest($7::bigint[]),
			unnest($8::text[]), now()
		ON CONFLICT (instrument_id, price_date) DO UPDATE SET
			open = EXCLUDED.open,
			high = EXCLUDED.high,
			low = EXCLUDED.low,
			close = EXCLUDED.close,
			volume = EXCLUDED.volume,
			data_provider = EXCLUDED.data_provider,
			fetched_at = EXCLUDED.fetched_at
	`, pq.Array(instIDs), pq.Array(dates), pq.Array(opens),
		pq.Array(highs), pq.Array(lows), pq.Array(closes),
		pq.Array(volumes), pq.Array(providers))
	if err != nil {
		return fmt.Errorf("upsert prices: %w", err)
	}
	return nil
}
