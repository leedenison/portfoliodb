# Performance Chart Design Decisions

## Charting library: Recharts

Recharts was chosen for its React-native API (composable components rather than
imperative D3 wrappers), small bundle size, and built-in responsive container.
It handles time-series data well and supports custom tooltip/dot renderers needed
for the unpriced-instrument indicators.

## Valuation computation

Daily portfolio values are computed server-side in a single SQL query using five
CTEs:

1. **portfolio_txs** -- portfolio-matched transactions grouped by
   (instrument, date) with net daily quantity.
2. **cumulative** -- window function producing running position per instrument.
3. **date_series** -- `generate_series` for every calendar date in the range.
4. **daily_holdings** -- LATERAL subquery forward-filling the last known
   position for each instrument on each date.
5. **gapfilled_prices** -- TimescaleDB `time_bucket_gapfill()` with `locf()`
   to forward-fill closing prices across weekends and holidays.

The final SELECT joins holdings with prices and aggregates
`SUM(qty * close)` per date.

## TimescaleDB usage

`time_bucket_gapfill('1 day', price_date, dateFrom, dateTo)` generates a row
for every date in the range per instrument, even when `eod_prices` has no row
(weekends, holidays). `locf(close)` forward-fills the last known closing price
into those generated gap rows. This gives a continuous price series without
application-level logic -- a holding valued at $100 on Friday correctly shows
$100 on Saturday/Sunday rather than NULL.

Both are core TimescaleDB functions (not toolkit), available in the
`timescale/timescaledb:latest-pg16` image used by the project.

## Unpriced instrument handling

An instrument is "unpriced" only when it has never had a price up to that date
(i.e., `locf()` returns NULL because there is no prior observation). Weekend
gaps where `locf()` successfully fills are NOT reported as unpriced.

On the chart, unpriced dates are indicated with orange dots and the custom
tooltip lists the affected instrument names. An info banner appears above the
chart when any point has unpriced instruments.

## Period selection

Periods (3M, 6M, 1Y, 2Y, 5Y) are calendar-based, computed relative to today.
The server returns data for all calendar dates, not just trading days, which
gives a uniform x-axis.

## Weekend and holiday treatment

Holdings are forward-filled from the last transaction date via a LATERAL
subquery. Prices are forward-filled via `locf()`. Together this means weekends
and holidays show flat segments (last known value) rather than gaps.
