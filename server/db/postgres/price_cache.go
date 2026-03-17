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
func (p *Postgres) PriceGaps(ctx context.Context, opts db.HeldRangesOpts) ([]db.InstrumentDateRanges, error) {
	return nil, fmt.Errorf("not implemented")
}
