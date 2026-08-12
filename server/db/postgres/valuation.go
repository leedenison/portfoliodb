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
// portfolioMode selects the txSource fragment that scopes and filters the postings:
// a join to portfolio_matched_txs, or a bare user_id predicate.
//
// The query uses $1 for the scope ID (portfolio or user), $2/$3 for the
// half-open [from, before) date range,
// and $4 for displayCurrency.
func valuationQuery(portfolioMode bool) string {
	var txSource string
	// USER postings are valued, and so are the TRANSFER_CLEARING legs of a matched pair
	// whose other side is in scope. Income, charges and residuals are not positions, and
	// an unmatched clearing balance is not one either: including it would assert that
	// money in transit is coming back to an account we hold, which is the thing we do
	// not know.
	//
	// A matched pair holds its value flat rather than needing anything carried over the
	// days in between. The departure group is a negative leg and a positive clearing
	// leg, the arrival group the mirror, so admitting both makes each group contribute
	// exactly zero to daily_qty on its own date and the running position never moves.
	// It generalises past the two-leg case: a journal charging a fee is USER -q,
	// EXPENSE +f, TRANSFER_CLEARING +q-f, which nets to -f -- correct, since the fee
	// really did leave.
	//
	// Which pairs a portfolio values is portfolio_in_flight_txs, defined beside
	// portfolio_matched_txs because it is the same question about membership. The
	// external flow query reads the same view the other way round, so the two cannot
	// disagree about what nets.
	//
	// Written as an OR with account_type = 'USER' first so the subquery stays a subplan
	// the scan skips for the USER postings that dominate the table, as
	// residual_balances.go does for its anti-join.
	if portfolioMode {
		txSource = `
    FROM txs t
    INNER JOIN portfolio_matched_txs m ON m.tx_id = t.id AND m.portfolio_id = $1
    WHERE t.timestamp::date < $3
      AND (t.account_type = 'USER'
           OR EXISTS (SELECT 1 FROM portfolio_in_flight_txs f
                      WHERE f.tx_id = t.id AND f.portfolio_id = $1))`
	} else {
		// A match alone is the whole test here. Both groups belong to the same user by
		// construction -- the matcher partitions its candidates by user before proposing
		// anything -- and every account of the user is in scope, so there is no
		// membership left to ask about and no need for the view.
		txSource = `
    FROM txs t
    WHERE t.user_id = $1 AND t.timestamp::date < $3
      AND (t.account_type = 'USER'
           OR (t.account_type = 'TRANSFER_CLEARING' AND EXISTS (
               SELECT 1 FROM transfer_matches tm
               WHERE tm.instrument_id = t.instrument_id
                 AND (tm.from_group_id = t.group_id OR tm.to_group_id = t.group_id))))`
	}

	return `
WITH portfolio_txs AS (
    SELECT
        t.instrument_id,
        t.instrument_description,
        t.timestamp::date AS tx_date,
        SUM(t.split_adjusted_quantity) AS daily_qty,
        -- How many of these postings may have rounded when they were converted
        -- into today's share count, which is what bounds the running position's
        -- test against zero further down. A row whose adjusted quantity equals
        -- its raw one converted by 1/1 and cannot have rounded at all. See
        -- qty_is_zero.
        COUNT(*) FILTER (WHERE t.split_adjusted_quantity <> t.quantity)::int AS daily_inexact` + txSource + `
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
        SUM(daily_qty) AS daily_qty,
        SUM(daily_inexact)::int AS daily_inexact
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
        ) AS position,
        -- Accumulated over the same window as the position it bounds: every
        -- posting that has entered the running sum can have contributed a
        -- rounding to it.
        SUM(daily_inexact) OVER (
            PARTITION BY instrument_id, instrument_description
            ORDER BY tx_date
            ROWS UNBOUNDED PRECEDING
        )::int AS inexact
    FROM merged_txs
),
-- generate_series reports a fixed estimate of 1000 rows whatever it will
-- actually return, having no statistics to estimate from, and every join above
-- the grid is costed from that. Replacing it with a calendar table was tried and
-- reverted: the table estimated the same window at 1847 rows against 1827, but
-- the estimate at the join above the grid only moved from 111 to 205 against
-- 91,350, the plan shapes did not change and neither did the timing. The error
-- is not the leaf's. It is the range join in daily_holdings, which postgres has
-- no selectivity model for, multiplying a near-zero guess onto whatever the leaf
-- hands it -- so a better leaf buys a proportionally better wrong answer. The
-- table would also have bounded the valuable range and pinned the driver, since
-- the histogram only reaches a planner that can see the bounds and lib/pq is
-- what keeps these queries on custom plans.
date_series AS (
    SELECT d::date AS val_date
    FROM generate_series($2::date, $3::date - 1, '1 day'::interval) d
),
-- A position holds until a transaction changes it, so each running total covers
-- the days from its own transaction up to the next one. Naming that span is what
-- turns the day grid into a join.
--
-- It was once a per-day lookup for the latest transaction at or before each
-- date, which asked the same question of the same rows once per cell of the
-- grid: at 50 instruments over 5 years with 20 transactions each that was 91,350
-- rescans of the whole portfolio's transactions, and 85% of the query. The
-- window here runs over the transactions instead -- 1,000 rows, not 91,350 --
-- and the grid falls out of joining the spans to the dates they cover.
--
-- GREATEST clamps a span opening before the window to the window's own start,
-- which is how a position opened earlier is carried in without a lookup for it.
-- Where two transactions both predate the window the earlier one's span comes
-- out empty (from >= before) and contributes nothing, leaving the last one
-- before the window holding the open, which is correct.
holding_spans AS (
    SELECT
        instrument_id,
        instrument_description,
        position,
        inexact,
        GREATEST(tx_date, $2::date) AS from_date,
        COALESCE(
            lead(tx_date) OVER (
                PARTITION BY instrument_id, instrument_description
                ORDER BY tx_date
            ),
            $3::date
        ) AS before_date
    FROM cumulative
),
-- An inner join, so a date before an instrument's first transaction has no row
-- rather than a row with a NULL position. Both read the same downstream: valued
-- applies NOT qty_is_zero, and qty_is_zero answers true for NULL, so a row the
-- old shape produced here was always discarded there. Not producing it is the
-- same series, reached without materialising a cell per instrument per day for
-- history that has not started yet.
daily_holdings AS (
    SELECT
        ds.val_date,
        hs.instrument_id,
        hs.instrument_description,
        hs.position AS qty,
        hs.inexact
    FROM holding_spans hs
    JOIN date_series ds
        ON ds.val_date >= hs.from_date AND ds.val_date < hs.before_date
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
-- Carry-forward is bounded by coverage, so a delisted instrument's last close
-- stops at the end of the span rather than being held for ever. Partitioning by
-- span_from below makes that fall out of the grouping instead of needing a
-- guard, which is why the span's lower bound is carried down rather than just
-- used to filter. merged_price_coverage unions the per-plugin spans: for
-- "should this day have a price" it does not matter which provider answered.
covered_grid AS (
    SELECT mc.instrument_id, ds.val_date, mc.covered_from AS span_from
    FROM merged_price_coverage mc
    JOIN date_series ds
        ON ds.val_date >= mc.covered_from AND ds.val_date < mc.covered_before
    WHERE mc.instrument_id IN (SELECT instrument_id FROM price_instruments)
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
        -- This CASE is the single place the query crosses from exact to
        -- approximate. Quantities and prices are exact decimals, but valuing a
        -- position divides by an FX rate and a division has no exact decimal
        -- result, so the value is an estimate from here on. The cast says so, and
        -- it is applied to the operands rather than to the result: casting the
        -- result would have Postgres compute sixteen significant digits of numeric
        -- division for every instrument on every calendar day and then discard
        -- them. See docs/spec/performance.md and
        -- docs/adr/0026-exact-decimals-bounded-by-closure.md.
        CASE
            -- Unidentified instrument: always unpriced.
            WHEN dh.instrument_id IS NULL THEN NULL
            -- Cash in display currency: implicit price 1.0, no FX needed.
            WHEN inst.asset_class = 'CASH' AND COALESCE(inst.currency, $4) = $4
                THEN dh.qty::double precision
            -- Cash in foreign currency: implicit price 1.0, convert via FX rate.
            WHEN inst.asset_class = 'CASH' THEN
                CASE
                    WHEN $4 = 'USD' THEN
                        CASE WHEN fr.rate IS NOT NULL
                            THEN dh.qty::double precision * fr.rate::double precision
                            ELSE NULL
                        END
                    ELSE
                        CASE WHEN dfr.rate IS NOT NULL
                                AND (COALESCE(inst.currency, 'USD') = 'USD' OR fr.rate IS NOT NULL)
                            THEN dh.qty::double precision
                                * COALESCE(fr.rate::double precision, 1.0)
                                / dfr.rate::double precision
                            ELSE NULL
                        END
                END
            -- Non-cash with no price: unpriced.
            WHEN gp.close IS NULL THEN NULL
            -- Instrument currency IS the display currency (or NULL): no conversion.
            WHEN COALESCE(inst.currency, $4) = $4
                THEN dh.qty::double precision * gp.close::double precision
            -- Display = USD: fx_rate = BASEUSD_rate.
            WHEN $4 = 'USD' THEN
                CASE WHEN fr.rate IS NOT NULL
                    THEN dh.qty::double precision
                        * gp.close::double precision
                        * fr.rate::double precision
                    ELSE NULL  -- missing FX rate -> unpriced
                END
            -- Display != USD: fx_rate = BASEUSD_rate / DISPLAYUSD_rate.
            -- For USD-denominated instruments, BASEUSD = 1.0 so fx_rate = 1.0 / DISPLAYUSD.
            ELSE
                CASE WHEN dfr.rate IS NOT NULL
                        AND (COALESCE(inst.currency, 'USD') = 'USD' OR fr.rate IS NOT NULL)
                    THEN dh.qty::double precision
                        * gp.close::double precision
                        * COALESCE(fr.rate::double precision, 1.0)
                        / dfr.rate::double precision
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
    WHERE NOT qty_is_zero(dh.qty, dh.inexact)
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
