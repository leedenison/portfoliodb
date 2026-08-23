# Display Currency

## Overview

Portfolio valuation requires summing the value of all holdings into a single
number. When a portfolio contains instruments denominated in different
currencies, summing `qty * close` directly produces a meaningless result
because it mixes currency units.

Display currency solves this by converting every holding's value into the
user's chosen currency before aggregation. The converted formula is:

    total_value = SUM(qty * close * fx_rate)

where `fx_rate` converts from the instrument's native currency to the display
currency.

## User preference

Each user has a display currency stored as `display_currency` on the `users`
table:

- Type: `TEXT NOT NULL DEFAULT 'USD'`
- Values: ISO 4217 three-letter currency codes (e.g. USD, EUR, GBP)
- Exposed via the gRPC API and the settings UI

The default is USD. Users can change it at any time; the change takes effect
on the next valuation request.

## FX pair instruments

FX rates are stored as synthetic instruments in the `instruments` table and
their daily rates go into `eod_prices`, reusing the entire price cache
pipeline (gap detection, coverage tracking, gapfilling, plugin fetching). See
adr/0006-fx-as-synthetic-instruments.md.

Each FX pair instrument has:

| Field | Value | Example |
|-------|-------|---------|
| `asset_class` | `'FX'` | `'FX'` |
| `currency` | Quote currency (always `'USD'`) | `'USD'` |
| `name` | `'BASE/USD'` | `'EUR/USD'` |
| Identifier type | `FX_PAIR` | `FX_PAIR` |
| Identifier value | `BASEUSD` (no slash) | `EURUSD` |

The `close` column in `eod_prices` stores how many USD one unit of the base
currency buys. For example, if EUR/USD closes at 1.08, then 1 EUR = 1.08 USD.

FX instruments are distinct from the existing CASH instruments (seeded in
migration 002). CASH instruments represent currency holdings (e.g. a US Dollar
cash balance). FX instruments represent exchange rate time series between two
currencies.

## FX pair direction and cross-rates

All FX pairs are stored with USD as the quote currency (BASE/USD). This means
only one rate per foreign currency is stored, regardless of how many different
display currencies users choose (see adr/0006-fx-as-synthetic-instruments.md).

### Conversion formulas

**Display currency is USD:**

    value_usd = qty * close * BASEUSD_rate

where BASEUSD_rate is the `close` from the listing's currency FX pair
(e.g. EURUSD close for a EUR-denominated instrument).

**Display currency is non-USD (e.g. EUR):**

    value_eur = qty * close * (BASEUSD_rate / EURUSD_rate)

This uses two stored USD-quoted rates to compute the cross-rate. For example,
a GBP instrument displayed in EUR:

    value_eur = qty * close_gbp * (GBPUSD_rate / EURUSD_rate)

**Instrument already in display currency:**

    fx_rate = 1.0  (no conversion needed, no FX lookup)

**Instrument in USD, display currency is USD:**

    fx_rate = 1.0  (no conversion needed)

**Instrument in USD, display currency is non-USD (e.g. EUR):**

    value_eur = qty * close_usd * (1.0 / EURUSD_rate)

Since the instrument is already in USD, the BASEUSD rate is 1.0 by definition.

## Determining required FX pairs

The system determines which FX pair instruments are needed by scanning the
currencies of all held listings:

1. From `HeldRanges`, identify all listings with non-zero positions.
2. Look up each listing's `currency` from `instrument_listings`. That is where a
   currency lives: it is a fact about the line, not about the security above it.
3. For each currency C where C != USD, ensure an FX pair instrument C/USD
   exists (with identifier `FX_PAIR` / value `CUSD`).
4. If it does not exist, create it on demand.

FX pair instruments are shared across all users (like all instrument and price
data). A migration seeds FX instruments for the same currencies already
seeded as CASH instruments in migration 002.

## FX rate fetching

FX pair instruments participate in the price cache pipeline:

1. `FXGaps` (a new `PriceCacheDB` method) computes date ranges where FX rates
   are needed but not yet cached. It works by mapping held instrument
   currencies to FX pair instrument IDs and computing the union of held ranges
   per currency, then subtracting existing coverage.
2. The worker's `runCycle` calls `FXGaps` in addition to `PriceGaps` and
   processes the resulting gaps through the same plugin loop.
3. FX instruments have `asset_class = 'FX'`, so only plugins that accept this
   asset class will handle them.

The Massive price plugin is extended to support FX:

- `AcceptableAssetClasses()` adds `'FX'`
- `SupportedIdentifierTypes()` adds `'FX_PAIR'`
- `tickerForAssetClass` formats the FX_PAIR identifier value with a `C:`
  prefix for the Polygon.io forex endpoint (e.g. `C:EURUSD`)
- `AcceptableCurrencies()` remains `{"USD": true}`, tested against the listing's
  own currency -- an FX pair's listing is quoted in USD (the quote currency), so
  it passes this filter

## Conversion in valuation

The valuation query (used by both `GetPortfolioValuation` and
`GetUserValuation`) is modified to apply FX conversion:

1. A new CTE `gapfilled_fx_rates` is added, structured identically to
   `gapfilled_prices` but selecting from `eod_prices` for FX pair instrument
   IDs. It uses the same `time_bucket_gapfill` + `locf` pattern.
2. Each holding is joined to its listing's currency, and then to the
   appropriate FX rate(s).
3. The final aggregation becomes:

       SUM(qty * close * COALESCE(fx_rate, 1.0))

   where `fx_rate` is derived from the stored USD-quoted rates as described
   in the cross-rate formulas above.

Both methods gain a `displayCurrency` parameter. The query uses this to
determine which FX conversions are needed and to compute cross-rates when the
display currency is not USD.

## Edge cases

**Missing FX rate:** When a held instrument requires an FX conversion but no
rate is available (the FX pair instrument has no price data for that date),
the instrument is treated as unpriced. It appears in `unpriced_instruments`
and shows as an orange dot on the performance chart. Its value is excluded
from the total rather than using a stale or assumed rate.

**Holding with no line:** a holding the query cannot attribute to a currency
line is **unpriced**, not converted. It used to be treated as already in the
display currency, which valued it at an implied FX rate of 1; a value nobody can
state is reported as missing rather than guessed. See
adr/0068-a-listing-is-a-currency-of-a-security.md and
adr/0072-a-posting-names-a-security-and-a-line.md.

Two shapes reach it, and both leave `txs.listing_id` null. The security is quoted
in more than one currency and no posting of the holding said which line it is on.
Or nobody has named a line for it at all, so it has none to be on.

In both the quantity is right and the currency is unknown. Picking a line would
value the position at a currency nobody stated, which is the failure this level
exists to remove.

Cash does not reach it: a cash instrument resolves through a `CURRENCY`
identifier, so it always has exactly one line and that line always has a
currency.

**Listing currency equals display currency:** No FX lookup is needed;
`fx_rate = 1.0`. The query tests the line's currency against the display
currency directly and skips the conversion, so no FX rate row is needed and none
is looked for.

**USD instrument with non-USD display currency:** The BASEUSD rate is 1.0 by
definition (1 USD = 1 USD). The conversion simplifies to dividing by the
display currency's USD rate: `value = qty * close / DISPLAYUSD_rate`.

## Schema changes

The following schema changes are required:

1. **Asset class CHECK constraint:** Add `'FX'` to the allowed values in the
   `instruments` table CHECK constraint.
2. **ValidAssetClasses map:** Add `AssetClassFX = "FX"` to the Go constant
   and `ValidAssetClasses` map in `server/db/db.go`.
3. **FX_PAIR identifier type:** Add `"FX_PAIR"` to the valid identifier types
   in `server/identifier/hints.go`.
4. **display_currency column:** Add `display_currency TEXT NOT NULL DEFAULT
   'USD'` to the `users` table.
5. **Seed FX instruments:** A migration seeds FX pair instruments for the
   currencies already present in migration 002, with `FX_PAIR` identifiers.
6. **PriceCacheDB interface:** Add `FXGaps` method.
7. **Valuation API:** Add `displayCurrency` parameter to
   `GetPortfolioValuation` and `GetUserValuation` proto messages.
