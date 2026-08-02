package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/lib/pq"
)

// valuationQuery returns the full SQL for portfolio valuation with FX conversion.
// portfolioFilter is the WHERE clause fragment that scopes transactions:
//   - Portfolio mode: "INNER JOIN portfolio_matched_txs m ON m.tx_id = t.id AND m.portfolio_id = $1"
//   - User mode:      "WHERE t.user_id = $1 AND t.timestamp::date < $3"
//
// The query uses $1 for the scope ID (portfolio or user), $2/$3 for the
// half-open [from, before) date range,
// and $4 for displayCurrency.
func valuationQuery(portfolioMode bool) string {
	var txSource string
	if portfolioMode {
		txSource = `
    FROM txs t
    INNER JOIN portfolio_matched_txs m ON m.tx_id = t.id AND m.portfolio_id = $1
    WHERE t.timestamp::date < $3`
	} else {
		txSource = `
    FROM txs t
    WHERE t.user_id = $1 AND t.timestamp::date < $3`
	}

	return `
WITH portfolio_txs AS (
    SELECT
        t.instrument_id,
        t.instrument_description,
        t.timestamp::date AS tx_date,
        SUM(t.split_adjusted_quantity) AS daily_qty` + txSource + `
    GROUP BY t.instrument_id, t.instrument_description, t.timestamp::date
),
-- Merge transactions by instrument_id for identified instruments so that
-- different descriptions for the same instrument net correctly. Unidentified
-- instruments (NULL instrument_id) are grouped by instrument_description.
merged_txs AS (
    SELECT
        instrument_id,
        CASE WHEN instrument_id IS NULL THEN instrument_description END AS instrument_description,
        tx_date,
        SUM(daily_qty) AS daily_qty
    FROM portfolio_txs
    GROUP BY instrument_id,
             CASE WHEN instrument_id IS NULL THEN instrument_description END,
             tx_date
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
    FROM merged_txs
),
date_series AS (
    SELECT d::date AS val_date
    FROM generate_series($2::date, $3::date - 1, '1 day'::interval) d
),
inst_list AS (
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
              AND c.instrument_description IS NOT DISTINCT FROM i.instrument_description
              AND c.tx_date <= ds.val_date
            ORDER BY c.tx_date DESC
            LIMIT 1
        ) AS qty
    FROM date_series ds
    CROSS JOIN inst_list i
),
-- Map held instruments to their FX pair instrument IDs (for currencies != display).
fx_instruments AS (
    SELECT DISTINCT
        inst.currency AS base_currency,
        fx_ii.instrument_id AS fx_instrument_id
    FROM instruments inst
    INNER JOIN instrument_identifiers fx_ii
        ON fx_ii.identifier_type = 'FX_PAIR'
        AND fx_ii.value = inst.currency || 'USD'
    WHERE inst.id = ANY(SELECT DISTINCT instrument_id FROM cumulative WHERE instrument_id IS NOT NULL)
      AND inst.currency IS NOT NULL
      AND inst.currency != 'USD'
),
-- The display currency's own pair (DISPLAY/USD), only when display != USD.
display_fx_instrument AS (
    SELECT ii.instrument_id
    FROM instrument_identifiers ii
    WHERE ii.identifier_type = 'FX_PAIR'
      AND ii.value = $4 || 'USD'
      AND $4 != 'USD'
),
-- Every instrument this query needs a price series for.
price_instruments AS (
    SELECT DISTINCT instrument_id FROM cumulative WHERE instrument_id IS NOT NULL
    UNION
    SELECT fx_instrument_id FROM fx_instruments
    UNION
    SELECT instrument_id FROM display_fx_instrument
),
-- Only real bars are stored, so the days a market was shut have no row. The
-- next four CTEs carry the last close forward over them.
--
-- Spans merged across plugins: for "should this day have a price" it does not
-- matter which provider answered.
coverage_spans AS (
    SELECT instrument_id,
        unnest(range_agg(daterange(covered_from, covered_before))) AS span
    FROM price_coverage
    WHERE instrument_id IN (SELECT instrument_id FROM price_instruments)
    GROUP BY instrument_id
),
-- Carry-forward is bounded by coverage, so a delisted instrument's last close
-- stops at the end of the span rather than being held for ever. Partitioning by
-- span below makes that fall out of the grouping instead of needing a guard.
covered_grid AS (
    SELECT cs.instrument_id, ds.val_date, lower(cs.span) AS span_from
    FROM coverage_spans cs
    JOIN date_series ds
        ON ds.val_date >= lower(cs.span) AND ds.val_date < upper(cs.span)
),
-- A window opening mid-span would otherwise read as unpriced until the first
-- bar inside it. One lookup per (instrument, span), not per day.
span_seeds AS (
    SELECT g.instrument_id, g.span_from, s.close, s.split_adjusted_close
    FROM (SELECT DISTINCT instrument_id, span_from FROM covered_grid) g
    CROSS JOIN LATERAL (
        SELECT ep.close, ep.split_adjusted_close
        FROM eod_prices ep
        WHERE ep.instrument_id = g.instrument_id
          AND ep.price_date >= g.span_from
          AND ep.price_date < $2::date
        ORDER BY ep.price_date DESC
        LIMIT 1
    ) s
),
price_points AS (
    -- Virtual point before the window start, seeding the carry-forward.
    SELECT instrument_id, span_from, ($2::date - 1) AS val_date, close, split_adjusted_close
    FROM span_seeds
    UNION ALL
    SELECT g.instrument_id, g.span_from, g.val_date, ep.close, ep.split_adjusted_close
    FROM covered_grid g
    LEFT JOIN eod_prices ep
        ON ep.instrument_id = g.instrument_id AND ep.price_date = g.val_date
),
-- PostgreSQL window functions have no IGNORE NULLS, so count() labels each run
-- that starts at a real bar and first_value() spreads that bar across the run.
price_islands AS (
    SELECT instrument_id, span_from, val_date, close, split_adjusted_close,
        count(close) OVER (PARTITION BY instrument_id, span_from ORDER BY val_date) AS island
    FROM price_points
),
filled_prices AS (
    SELECT instrument_id, val_date,
        first_value(close) OVER w AS close,
        first_value(split_adjusted_close) OVER w AS split_adjusted_close
    FROM price_islands
    WINDOW w AS (PARTITION BY instrument_id, span_from, island ORDER BY val_date)
),
prices AS (
    -- Adjusted quantity above pairs with adjusted close here: both are
    -- denominated in today's share count. Mixing one of each would scale the
    -- value by the split factor either side of an ex-date. See
    -- docs/spec/bitemporality.md.
    SELECT instrument_id, val_date, split_adjusted_close AS close
    FROM filled_prices
    WHERE val_date >= $2::date AND split_adjusted_close IS NOT NULL
),
-- FX rates for each base currency (BASE/USD close values).
-- Raw close, not split_adjusted_close: an exchange rate is not denominated in
-- a share count, so it has no basis to adjust and never carries splits.
fx_rates AS (
    SELECT fi.base_currency, fp.val_date, fp.close AS rate
    FROM fx_instruments fi
    JOIN filled_prices fp ON fp.instrument_id = fi.fx_instrument_id
    WHERE fp.val_date >= $2::date AND fp.close IS NOT NULL
),
-- Rate for the display currency (DISPLAY/USD), only when display != USD.
display_fx_rate AS (
    SELECT fp.val_date, fp.close AS rate
    FROM display_fx_instrument dfi
    JOIN filled_prices fp ON fp.instrument_id = dfi.instrument_id
    WHERE fp.val_date >= $2::date AND fp.close IS NOT NULL
),
-- Compute fx_rate per holding: converts from instrument currency to display currency.
valued AS (
    SELECT
        dh.val_date,
        dh.instrument_id,
        dh.instrument_description,
        inst.name AS instrument_name,
        inst.asset_class,
        dh.qty,
        gp.close,
        CASE
            -- Unidentified instrument: always unpriced.
            WHEN dh.instrument_id IS NULL THEN NULL
            -- Cash in display currency: implicit price 1.0, no FX needed.
            WHEN inst.asset_class = 'CASH' AND COALESCE(inst.currency, $4) = $4
                THEN dh.qty
            -- Cash in foreign currency: implicit price 1.0, convert via FX rate.
            WHEN inst.asset_class = 'CASH' THEN
                CASE
                    WHEN $4 = 'USD' THEN
                        CASE WHEN fr.rate IS NOT NULL THEN dh.qty * fr.rate ELSE NULL END
                    ELSE
                        CASE WHEN dfr.rate IS NOT NULL
                                AND (COALESCE(inst.currency, 'USD') = 'USD' OR fr.rate IS NOT NULL)
                            THEN dh.qty * COALESCE(fr.rate, 1.0) / dfr.rate
                            ELSE NULL
                        END
                END
            -- Non-cash with no price: unpriced.
            WHEN gp.close IS NULL THEN NULL
            -- Instrument currency IS the display currency (or NULL): no conversion.
            WHEN COALESCE(inst.currency, $4) = $4 THEN dh.qty * gp.close
            -- Display = USD: fx_rate = BASEUSD_rate.
            WHEN $4 = 'USD' THEN
                CASE WHEN fr.rate IS NOT NULL
                    THEN dh.qty * gp.close * fr.rate
                    ELSE NULL  -- missing FX rate -> unpriced
                END
            -- Display != USD: fx_rate = BASEUSD_rate / DISPLAYUSD_rate.
            -- For USD-denominated instruments, BASEUSD = 1.0 so fx_rate = 1.0 / DISPLAYUSD.
            ELSE
                CASE WHEN dfr.rate IS NOT NULL
                        AND (COALESCE(inst.currency, 'USD') = 'USD' OR fr.rate IS NOT NULL)
                    THEN dh.qty * gp.close * COALESCE(fr.rate, 1.0) / dfr.rate
                    ELSE NULL  -- missing base or display FX rate -> unpriced
                END
        END AS converted_value,
        -- Flag: needs FX conversion but rate is missing (applies to both cash and non-cash).
        CASE
            WHEN dh.instrument_id IS NOT NULL
                AND (gp.close IS NOT NULL OR inst.asset_class = 'CASH')
                AND COALESCE(inst.currency, $4) != $4
                AND (
                    ($4 = 'USD' AND fr.rate IS NULL)
                    OR ($4 != 'USD' AND (
                        dfr.rate IS NULL
                        OR (fr.rate IS NULL AND COALESCE(inst.currency, 'USD') != 'USD')
                    ))
                )
            THEN true
            ELSE false
        END AS fx_missing
    FROM daily_holdings dh
    LEFT JOIN prices gp
        ON gp.instrument_id = dh.instrument_id AND gp.val_date = dh.val_date
    LEFT JOIN instruments inst ON inst.id = dh.instrument_id
    LEFT JOIN fx_rates fr
        ON fr.base_currency = inst.currency AND fr.val_date = dh.val_date
    LEFT JOIN display_fx_rate dfr ON dfr.val_date = dh.val_date
    WHERE NOT qty_is_zero(dh.qty)
)
SELECT
    val_date,
    COALESCE(SUM(converted_value), 0) AS total_value,
    COALESCE(
        array_agg(DISTINCT COALESCE(instrument_name, instrument_description))
        FILTER (WHERE instrument_id IS NOT NULL AND close IS NULL
                  AND COALESCE(asset_class, '') != 'CASH'),
        '{}'
    ) || COALESCE(
        array_agg(DISTINCT COALESCE(instrument_name, instrument_description))
        FILTER (WHERE instrument_id IS NULL),
        '{}'
    ) || COALESCE(
        array_agg(DISTINCT COALESCE(instrument_name, instrument_description))
        FILTER (WHERE fx_missing),
        '{}'
    ) AS unpriced_instruments
FROM valued
GROUP BY val_date
ORDER BY val_date
`
}

// GetPortfolioValuation computes daily portfolio values over the half-open
// [dateFrom, dateBefore) range.
// Only real bars are stored, so prices are carried forward over non-trading days
// at read time, bounded by price_coverage. Holdings are forward-filled from the
// last transaction date. Holdings are converted to displayCurrency via FX rates.
func (p *Postgres) GetPortfolioValuation(ctx context.Context, portfolioID string, dateFrom, dateBefore time.Time, displayCurrency string) ([]db.ValuationPoint, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return nil, fmt.Errorf("invalid portfolio id: %w", err)
	}
	return p.queryValuation(ctx, valuationQuery(true), portUUID, dateFrom, dateBefore, displayCurrency)
}

// GetUserValuation computes daily portfolio values over the half-open
// [dateFrom, dateBefore) range
// for all of a user's transactions (no portfolio filter).
func (p *Postgres) GetUserValuation(ctx context.Context, userID string, dateFrom, dateBefore time.Time, displayCurrency string) ([]db.ValuationPoint, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	return p.queryValuation(ctx, valuationQuery(false), userUUID, dateFrom, dateBefore, displayCurrency)
}

func (p *Postgres) queryValuation(ctx context.Context, q string, scopeID uuid.UUID, dateFrom, dateBefore time.Time, displayCurrency string) ([]db.ValuationPoint, error) {
	rows, err := p.q.QueryxContext(ctx, q, scopeID, dateFrom, dateBefore, displayCurrency)
	if err != nil {
		return nil, fmt.Errorf("valuation query: %w", err)
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
