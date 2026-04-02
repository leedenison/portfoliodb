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
//   - User mode:      "WHERE t.user_id = $1 AND t.timestamp::date <= $3"
//
// The query uses $1 for the scope ID (portfolio or user), $2/$3 for date range,
// and $4 for displayCurrency.
func valuationQuery(portfolioMode bool) string {
	var txSource string
	if portfolioMode {
		txSource = `
    FROM txs t
    INNER JOIN portfolio_matched_txs m ON m.tx_id = t.id AND m.portfolio_id = $1
    WHERE t.timestamp::date <= $3`
	} else {
		txSource = `
    FROM txs t
    WHERE t.user_id = $1 AND t.timestamp::date <= $3`
	}

	return `
WITH portfolio_txs AS (
    SELECT
        t.instrument_id,
        t.instrument_description,
        t.timestamp::date AS tx_date,
        SUM(t.quantity) AS daily_qty` + txSource + `
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
    FROM generate_series($2::date, $3::date, '1 day'::interval) d
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
prices AS (
    SELECT instrument_id, price_date AS val_date, close
    FROM eod_prices
    WHERE instrument_id = ANY(SELECT DISTINCT instrument_id FROM cumulative WHERE instrument_id IS NOT NULL)
      AND price_date >= $2::date
      AND price_date <= $3::date
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
-- FX rates for each base currency (BASE/USD close values).
fx_rates AS (
    SELECT fi.base_currency, ep.price_date AS val_date, ep.close AS rate
    FROM fx_instruments fi
    JOIN eod_prices ep ON ep.instrument_id = fi.fx_instrument_id
    WHERE ep.price_date >= $2::date
      AND ep.price_date <= $3::date
),
-- Rate for the display currency (DISPLAY/USD), only when display != USD.
display_fx_rate AS (
    SELECT ep.price_date AS val_date, ep.close AS rate
    FROM eod_prices ep
    INNER JOIN instrument_identifiers ii
        ON ii.instrument_id = ep.instrument_id
        AND ii.identifier_type = 'FX_PAIR'
        AND ii.value = $4 || 'USD'
    WHERE $4 != 'USD'
      AND ep.price_date >= $2::date
      AND ep.price_date <= $3::date
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
    WHERE dh.qty IS NOT NULL AND dh.qty != 0
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

// GetPortfolioValuation computes daily portfolio values over [dateFrom, dateTo].
// Prices (including synthetic LOCF rows for non-trading days) are joined
// directly from eod_prices. Holdings are forward-filled from the last
// transaction date. Holdings are converted to displayCurrency via FX rates.
func (p *Postgres) GetPortfolioValuation(ctx context.Context, portfolioID string, dateFrom, dateTo time.Time, displayCurrency string) ([]db.ValuationPoint, error) {
	portUUID, err := uuid.Parse(portfolioID)
	if err != nil {
		return nil, fmt.Errorf("invalid portfolio id: %w", err)
	}
	return p.queryValuation(ctx, valuationQuery(true), portUUID, dateFrom, dateTo, displayCurrency)
}

// GetUserValuation computes daily portfolio values over [dateFrom, dateTo]
// for all of a user's transactions (no portfolio filter).
func (p *Postgres) GetUserValuation(ctx context.Context, userID string, dateFrom, dateTo time.Time, displayCurrency string) ([]db.ValuationPoint, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, fmt.Errorf("invalid user id: %w", err)
	}
	return p.queryValuation(ctx, valuationQuery(false), userUUID, dateFrom, dateTo, displayCurrency)
}

func (p *Postgres) queryValuation(ctx context.Context, q string, scopeID uuid.UUID, dateFrom, dateTo time.Time, displayCurrency string) ([]db.ValuationPoint, error) {
	rows, err := p.q.QueryxContext(ctx, q, scopeID, dateFrom, dateTo, displayCurrency)
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
