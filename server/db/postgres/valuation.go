package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/lib/pq"
)

// GetPortfolioValuation computes daily portfolio values over [dateFrom, dateTo].
// Uses TimescaleDB time_bucket_gapfill + locf to forward-fill prices across
// weekends/holidays and a LATERAL join to forward-fill holdings from the last
// transaction date.
func (p *Postgres) GetPortfolioValuation(ctx context.Context, portfolioID string, dateFrom, dateTo time.Time, displayCurrency string) ([]db.ValuationPoint, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return nil, fmt.Errorf("invalid portfolio id: %w", err)
	}

	const q = `
WITH portfolio_txs AS (
    SELECT
        t.instrument_id,
        t.instrument_description,
        t.timestamp::date AS tx_date,
        SUM(t.quantity) AS daily_qty
    FROM txs t
    INNER JOIN portfolio_matched_txs m ON m.tx_id = t.id AND m.portfolio_id = $1
    WHERE t.timestamp::date <= $3
    GROUP BY t.instrument_id, t.instrument_description, t.timestamp::date
),
cumulative AS (
    SELECT
        instrument_id,
        instrument_description,
        tx_date,
        SUM(daily_qty) OVER (
            PARTITION BY instrument_id, instrument_description
            ORDER BY tx_date
            ROWS UNBOUNDED PRECEDING
        ) AS position
    FROM portfolio_txs
),
date_series AS (
    SELECT d::date AS val_date
    FROM generate_series($2::date, $3::date, '1 day'::interval) d
),
instruments AS (
    SELECT DISTINCT instrument_id, instrument_description
    FROM cumulative
),
daily_holdings AS (
    SELECT
        ds.val_date,
        i.instrument_id,
        i.instrument_description,
        (
            SELECT c.position
            FROM cumulative c
            WHERE c.instrument_id IS NOT DISTINCT FROM i.instrument_id
              AND c.instrument_description = i.instrument_description
              AND c.tx_date <= ds.val_date
            ORDER BY c.tx_date DESC
            LIMIT 1
        ) AS qty
    FROM date_series ds
    CROSS JOIN instruments i
),
gapfilled_prices AS (
    SELECT
        instrument_id,
        time_bucket_gapfill('1 day', price_date, $2::date, $3::date) AS val_date,
        locf(avg(close)) AS close
    FROM eod_prices
    WHERE instrument_id = ANY(SELECT DISTINCT instrument_id FROM cumulative WHERE instrument_id IS NOT NULL)
      AND price_date >= ($2::date - INTERVAL '30 days')
      AND price_date <= $3::date
    GROUP BY instrument_id, val_date
)
SELECT
    dh.val_date,
    COALESCE(SUM(dh.qty * gp.close) FILTER (WHERE gp.close IS NOT NULL), 0) AS total_value,
    COALESCE(
        array_agg(DISTINCT dh.instrument_description)
        FILTER (WHERE dh.qty != 0 AND dh.instrument_id IS NOT NULL AND gp.close IS NULL),
        '{}'
    ) || COALESCE(
        array_agg(DISTINCT dh.instrument_description)
        FILTER (WHERE dh.qty != 0 AND dh.instrument_id IS NULL),
        '{}'
    ) AS unpriced_instruments
FROM daily_holdings dh
LEFT JOIN gapfilled_prices gp
    ON gp.instrument_id = dh.instrument_id AND gp.val_date = dh.val_date
WHERE dh.qty IS NOT NULL AND dh.qty != 0
GROUP BY dh.val_date
ORDER BY dh.val_date
`

	rows, err := p.q.QueryxContext(ctx, q, portUUID, dateFrom, dateTo)
	if err != nil {
		return nil, fmt.Errorf("get portfolio valuation: %w", err)
	}
	defer rows.Close()

	var points []db.ValuationPoint
	for rows.Next() {
		var pt db.ValuationPoint
		var unpriced pq.StringArray
		if err := rows.Scan(&pt.Date, &pt.TotalValue, &unpriced); err != nil {
			return nil, fmt.Errorf("scan valuation point: %w", err)
		}
		pt.UnpricedInstruments = filterEmpty(unpriced)
		points = append(points, pt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate valuation rows: %w", err)
	}
	return points, nil
}

// GetUserValuation computes daily portfolio values over [dateFrom, dateTo]
// for all of a user's transactions (no portfolio filter).
func (p *Postgres) GetUserValuation(ctx context.Context, userID string, dateFrom, dateTo time.Time, displayCurrency string) ([]db.ValuationPoint, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}

	const q = `
WITH portfolio_txs AS (
    SELECT
        t.instrument_id,
        t.instrument_description,
        t.timestamp::date AS tx_date,
        SUM(t.quantity) AS daily_qty
    FROM txs t
    WHERE t.user_id = $1 AND t.timestamp::date <= $3
    GROUP BY t.instrument_id, t.instrument_description, t.timestamp::date
),
cumulative AS (
    SELECT
        instrument_id,
        instrument_description,
        tx_date,
        SUM(daily_qty) OVER (
            PARTITION BY instrument_id, instrument_description
            ORDER BY tx_date
            ROWS UNBOUNDED PRECEDING
        ) AS position
    FROM portfolio_txs
),
date_series AS (
    SELECT d::date AS val_date
    FROM generate_series($2::date, $3::date, '1 day'::interval) d
),
instruments AS (
    SELECT DISTINCT instrument_id, instrument_description
    FROM cumulative
),
daily_holdings AS (
    SELECT
        ds.val_date,
        i.instrument_id,
        i.instrument_description,
        (
            SELECT c.position
            FROM cumulative c
            WHERE c.instrument_id IS NOT DISTINCT FROM i.instrument_id
              AND c.instrument_description = i.instrument_description
              AND c.tx_date <= ds.val_date
            ORDER BY c.tx_date DESC
            LIMIT 1
        ) AS qty
    FROM date_series ds
    CROSS JOIN instruments i
),
gapfilled_prices AS (
    SELECT
        instrument_id,
        time_bucket_gapfill('1 day', price_date, $2::date, $3::date) AS val_date,
        locf(avg(close)) AS close
    FROM eod_prices
    WHERE instrument_id = ANY(SELECT DISTINCT instrument_id FROM cumulative WHERE instrument_id IS NOT NULL)
      AND price_date >= ($2::date - INTERVAL '30 days')
      AND price_date <= $3::date
    GROUP BY instrument_id, val_date
)
SELECT
    dh.val_date,
    COALESCE(SUM(dh.qty * gp.close) FILTER (WHERE gp.close IS NOT NULL), 0) AS total_value,
    COALESCE(
        array_agg(DISTINCT dh.instrument_description)
        FILTER (WHERE dh.qty != 0 AND dh.instrument_id IS NOT NULL AND gp.close IS NULL),
        '{}'
    ) || COALESCE(
        array_agg(DISTINCT dh.instrument_description)
        FILTER (WHERE dh.qty != 0 AND dh.instrument_id IS NULL),
        '{}'
    ) AS unpriced_instruments
FROM daily_holdings dh
LEFT JOIN gapfilled_prices gp
    ON gp.instrument_id = dh.instrument_id AND gp.val_date = dh.val_date
WHERE dh.qty IS NOT NULL AND dh.qty != 0
GROUP BY dh.val_date
ORDER BY dh.val_date
`

	rows, err := p.q.QueryxContext(ctx, q, userUUID, dateFrom, dateTo)
	if err != nil {
		return nil, fmt.Errorf("get user valuation: %w", err)
	}
	defer rows.Close()

	var points []db.ValuationPoint
	for rows.Next() {
		var pt db.ValuationPoint
		var unpriced pq.StringArray
		if err := rows.Scan(&pt.Date, &pt.TotalValue, &unpriced); err != nil {
			return nil, fmt.Errorf("scan valuation point: %w", err)
		}
		pt.UnpricedInstruments = filterEmpty(unpriced)
		points = append(points, pt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate valuation rows: %w", err)
	}
	return points, nil
}

// filterEmpty removes empty strings from a slice.
func filterEmpty(ss []string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
