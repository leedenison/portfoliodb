# Portfolio Valuation and Performance Chart

The front end charts portfolio value over time with Recharts (see
adr/0008-recharts-charting.md).

## Valuation computation

Daily portfolio values are computed server-side in a single SQL query using five
CTEs:

1. **portfolio_txs** -- portfolio-matched transactions grouped by
   (instrument, date) with net daily split-adjusted quantity.
2. **cumulative** -- window function producing running position per instrument.
3. **date_series** -- `generate_series` for every calendar date in the range.
4. **daily_holdings** -- LATERAL subquery forward-filling the last known
   position for each instrument on each date.
5. **gapfilled_prices** -- TimescaleDB `time_bucket_gapfill()` with `locf()`
   to forward-fill closing prices across weekends and holidays.

The final SELECT joins holdings with prices and aggregates
`SUM(qty * close)` per date, where both sides are split-adjusted (see
[Share count](#share-count) below).

## Exact and approximate parts

The running position per instrument is a sum, so accumulating it introduces no
error of its own. Its inputs are `split_adjusted_quantity`, though, which carries
the split adjustment's declared rounding scale (see
[Share count](#share-count) below), so the position is exact to that scale rather
than exact outright, with the bound growing in the number of contributing
postings. Summing the raw column instead is not an option: each posting is
denominated on its own `trade_date`, and postings recorded either side of
a split do not add.

In the value that rounding is immaterial and is tolerated. It is tracked in one
place: the test that decides whether a position is closed, which an error of one
unit in the last place is enough to fail. `qty_is_zero` therefore takes the count
of contributing postings that may have rounded -- those whose adjusted quantity
differs from their raw one -- and allows one unit in the last place for each,
which makes the test exact when no split falls in the window. The same test gates
holdings, the valuation day grid and the residual balance report. Where the
rounding is not tolerable at all -- the balance constraint, a checked holding
declaration -- the raw columns are read instead, per basis
(`holding_qty_in_basis`).

Valuing the position is approximate for a stronger reason: it multiplies by a
market price and divides by an FX rate, and a division has no exact decimal
result. The value is an estimate from that point on, and the query says so by
casting to `double precision` at the conversion, in the `valued` CTE. The cast is
applied to the operands, not to the result, and is the single place this query
crosses from exact to approximate.

`ValuationPoint.total_value` is a `double` on the wire for the same reason, and
so is any later return metric: geometric linking and an internal rate of return
are past the boundary too. See adr/0026-exact-decimals-bounded-by-closure.md and
adr/0027-decimal-values-cross-the-wire-as-strings.md.

## TimescaleDB usage

`time_bucket_gapfill('1 day', price_date, dateFrom, dateTo)` generates a row
for every date in the range per instrument, even when `eod_prices` has no row
(weekends, holidays). `locf(close)` forward-fills the last known closing price
into those generated gap rows, giving a continuous daily price series. For why
the system works in calendar days and forward-fills rather than using a trading
calendar, see adr/0007-calendar-day-valuation.md.

## Display currency conversion

The valuation computation above sums `qty * close` in each instrument's native
currency. Display currency conversion applies an FX rate to each holding before
aggregation so a mixed-currency portfolio sums to a single meaningful figure. FX
rates are stored as synthetic instruments (see adr/0006-fx-as-synthetic-instruments.md).

### FX rate CTE

A new CTE `gapfilled_fx_rates` is added to the valuation query. It selects
from `eod_prices` for FX pair instrument IDs (identified by joining
`instruments` with `asset_class = 'FX'` to `instrument_identifiers` with
type `FX_PAIR`). It uses the same `time_bucket_gapfill('1 day', ...)` +
`locf()` pattern as `gapfilled_prices`, producing a continuous daily FX rate
series per currency pair.

### Modified aggregation

The final SELECT changes from:

    SUM(qty * close)

to:

    SUM(qty * close * COALESCE(fx_rate, 1.0))

where `fx_rate` is derived from stored USD-quoted rates:

- **Display = USD:** `fx_rate = BASEUSD_rate` (direct lookup from
  `gapfilled_fx_rates` for the instrument's currency pair).
- **Display != USD (e.g. EUR):** `fx_rate = BASEUSD_rate / DISPLAYUSD_rate`
  (cross-rate from two stored pairs).
- **Instrument already in display currency:** no FX join needed; the LEFT
  JOIN produces NULL which COALESCE converts to 1.0.

### Query parameter change

`GetPortfolioValuation` and `GetUserValuation` gain a `displayCurrency`
parameter (string, ISO 4217). The query uses this to determine which FX pair
instruments to join and whether cross-rate arithmetic is needed. When omitted
or empty, it defaults to the user's stored `display_currency` preference.

### Unpriced handling for missing FX rates

When an instrument requires FX conversion but the rate is unavailable for a
given date (the `fx_rates` LEFT JOIN produces NULL), the instrument is reported
in `unpriced_instruments` alongside instruments missing price data. The same
orange-dot indicator appears on the chart.

## Unpriced instrument handling

An instrument is "unpriced" on a date when the carry-forward yields no price:
either no bar precedes the date inside the same covered span, or the date falls
outside every covered span. Weekend and holiday gaps that the carry-forward
fills are NOT reported as unpriced.

The coverage bound is what separates "we have no price for this day" from "the
last price we ever saw is still valid". Past the end of an instrument's
coverage -- a delisting, say -- it reads as unpriced rather than holding its
final close for ever. See prices.md and adr/0023-price-coverage-is-stored-not-inferred.md.

On the chart, unpriced dates are indicated with orange dots and the custom
tooltip lists the affected instrument names. An info banner appears above the
chart when any point has unpriced instruments.

## Period selection

Periods (3M, 6M, 1Y, 2Y, 5Y) are calendar-based, computed relative to today.
The server returns data for all calendar dates, not just trading days (see
adr/0007-calendar-day-valuation.md).

## Share count

Quantities and prices are paired on the same share count: the valuation reads
`split_adjusted_quantity` and `split_adjusted_close`, never one of each. Raw
quantity would not work, because `TX_TYPE=SPLIT` rows are dropped at ingestion
(see adr/0005-corporate-events-design.md) so the raw position never steps up at
a split, while the as-traded price does step down. Pairing them puts a cliff in
the chart at every forward split.

FX rates are the exception and are read raw: an exchange rate is not
denominated in a share count, so it has no basis to adjust.

Because split adjustment is bounded by today's date, the whole series is as-of
now -- the same request on a later day may return different values if a split
has become effective in between. See [bitemporality.md](bitemporality.md).

## Weekend and holiday treatment

Holdings are forward-filled from the last transaction date via a LATERAL
subquery. Prices are forward-filled via `locf()`. Together this means weekends
and holidays show flat segments (last known value) rather than gaps.
