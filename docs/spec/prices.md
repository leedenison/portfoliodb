# EOD Price Data Cache

## Overview

The demand-driven EOD (end-of-day) price data cache derives what price data is needed from a transaction history, tracks what data has already been cached, identifies gaps, and can produce a list of date ranges to fetch from external data providers.

This system does **not** fetch data from external APIs itself. It produces a plan of what to fetch. The actual fetching is out of scope.

---

## Data Model

### Table: `eod_prices`

The price cache.

| Column | Type | Description |
|--------|------|-------------|
| `instrument_id` | `UUID` NOT NULL | FK to `instruments` |
| `price_date` | `date` NOT NULL | The trading date |
| `open` | `numeric` | Opening price (nullable -- not all providers supply this) |
| `high` | `numeric` | High price (nullable) |
| `low` | `numeric` | Low price (nullable) |
| `close` | `numeric` NOT NULL | Closing price |
| `adjusted_close` | `numeric` | The **provider's own** adjusted close (nullable) |
| `volume` | `bigint` | Trading volume (nullable) |
| `data_provider` | `text` NOT NULL | Which provider supplied this row |
| `last_fetched_at` | `timestamptz` NOT NULL DEFAULT now() | When the row was last fetched. Staleness only; see [bitemporality.md](bitemporality.md#knowledge-time) |
| `share_count_basis` | `date` NOT NULL | The share count the raw OHLCV is denominated in. Defaults to `price_date` |

**Primary key:** `(instrument_id, price_date)`

**Index:** A TimescaleDB hypertable on `price_date`.

Every row is a bar a provider actually reported. Non-trading days have no row;
valuation carries the last close forward over them at read time, bounded by
`price_coverage`. See [Component 2](#component-2-coverage-inventory-pricecoverage).

### Table: `price_coverage`

Which date ranges have been answered for, per `(instrument, plugin)`.

| Column | Type | Description |
|--------|------|-------------|
| `instrument_id` | `UUID` NOT NULL | FK to `instruments`, `ON DELETE CASCADE` |
| `plugin_id` | `text` NOT NULL | Which plugin answered; `import` for coverage declared by an import |
| `covered_from` | `date` NOT NULL | Inclusive |
| `covered_before` | `date` NOT NULL | Exclusive; `CHECK (covered_before > covered_from)` |
| `last_fetched_at` | `timestamptz` NOT NULL | Staleness only. A merged span keeps the oldest constituent value |

**Primary key:** `(instrument_id, plugin_id, covered_from)`

Overlapping or abutting spans for one `(instrument, plugin)` are merged on
insert, so the table never holds two spans that touch. This mirrors
`corporate_event_coverage` exactly, and the merge is shared between them.

A span covers a range whether or not any bars came back: a provider that
answered "nothing here" -- a delisted, pre-IPO or untraded range -- has covered
it as authoritatively as one that returned a full series. Row presence cannot
express that, which is why coverage is stored rather than inferred. See
adr/0023-price-coverage-is-stored-not-inferred.md.

**Invariant.** Every `eod_prices` row lies within some `price_coverage` span for
its instrument. The converse deliberately does not hold. Coverage is written in
the same transaction as the rows, so no write path can store a price without
recording what it covers.

The table carries three closing prices and they are not interchangeable:

- `close` is the raw value as the provider supplied it, denominated in the share count the provider expressed it in.
- `split_adjusted_close` is derived by PortfolioDB from `close` and the known stock splits, denominated in today's share count. This is the value performance math uses. See [corporate-events.md](corporate-events.md#adjustment).
- `adjusted_close` is the provider's own adjusted figure, on the provider's basis and typically including dividend adjustment as well as splits. It is never an input to valuation -- it exists to cross-check the value PortfolioDB derives.

Which share count `close` is denominated in is recorded in `share_count_basis` and declared by its source, never inferred from `last_fetched_at`. See [bitemporality.md](bitemporality.md#share-count-basis).

A price plugin declares the denomination of the bars it returns on its `FetchResult`:

- `AsTraded` -- each bar is denominated in the share count current on its own date. Both current plugins return this: massive sends `?adjusted=false`, and EODHD's `/api/eod` OHLC is as-traded.
- `AsOfFetch` -- the provider back-adjusted the whole series to the share count current when it answered.

A carried-forward value keeps the basis of the bar it came from, since it is that bar's price rather than one of its own date. Import rows default to `price_date`, matching PortfolioDB's own export, and may declare `share_count_basis` when the file holds a back-adjusted series.

---

## Implementation

All components are implemented as Go functions in the database abstraction layer (`server/db`). The `PriceCacheDB` interface in `server/db/db.go` defines the contract; the Postgres implementation lives in `server/db/postgres/price_cache.go`.

Date ranges use the half-open `[From, Before)` convention with `time.Time` values at midnight UTC, matching PostgreSQL's `daterange` default (see adr/0018-half-open-date-intervals.md).

### Types

```go
// DateRange is a half-open [From, Before) date range. Both values are midnight UTC.
type DateRange struct {
    From   time.Time // inclusive
    Before time.Time // exclusive
}

// InstrumentDateRanges groups date ranges by instrument.
type InstrumentDateRanges struct {
    InstrumentID string
    Ranges       []DateRange
}

// HeldRangesOpts controls holdings range calculation.
type HeldRangesOpts struct {
    ExtendToToday bool // extend open positions to today
}
```

### Interface

```go
type PriceCacheDB interface {
    HeldRanges(ctx context.Context, opts HeldRangesOpts) ([]InstrumentDateRanges, error)
    PriceCoverage(ctx context.Context, instrumentIDs []string) ([]InstrumentDateRanges, error)
    PriceCoverageByPlugin(ctx context.Context, instrumentIDs []string) (map[string]map[string][]DateRange, error)
    PriceGaps(ctx context.Context, opts HeldRangesOpts) ([]InstrumentDateRanges, error)
    UpsertPrices(ctx context.Context, prices []EODPrice) error
    UpsertPricesForRange(ctx context.Context, instrumentID, provider string, bars []EODPrice, from, before time.Time, fetchedAt *time.Time) error
}
```

---

## Component 1: Holdings Calculator (`HeldRanges`)

### Purpose

Compute the date ranges during which any user held a non-zero position in each instrument, system-wide. This determines what price data is needed.

### Behaviour

1. Aggregate daily net quantity changes per instrument from the transaction history (system-wide, all users). Only transactions with a non-NULL `instrument_id` are included.
2. Compute the cumulative position per instrument using SQL window functions.
3. In Go, iterate the daily positions and detect zero-crossings:
   - `held_from` = the date the position first becomes non-zero.
   - `held_before` = the date the position returns to zero, OR today + 1 day (exclusive) if `ExtendToToday` is true and the position is still open.
4. Return the result as a slice of `InstrumentDateRanges`.

---

## Component 2: Coverage Inventory (`PriceCoverage`)

### Purpose

For each instrument, return the date ranges some plugin has answered for, as maximally merged non-overlapping ranges. This is read from `price_coverage`, never inferred from which dates happen to have rows.

### Behaviour

1. For each instrument (or specific instruments if `instrumentIDs` is non-empty), use PostgreSQL's `range_agg` to merge the stored spans across plugins.
2. Extract the lower and upper bounds as plain DATE values.
3. Return as a slice of `InstrumentDateRanges`.

Merging across plugins is right for "has anyone answered for this range". `PriceCoverageByPlugin` keeps the distinction, which is what the fetcher needs: a range one plugin declined must still be offered to another, and to a plugin configured later with deeper history.

### SQL approach

```sql
SELECT instrument_id, lower(r) AS range_from, upper(r) AS range_before
FROM (
    SELECT instrument_id,
        unnest(range_agg(daterange(covered_from, covered_before))) AS r
    FROM price_coverage
    WHERE ($1::uuid[] IS NULL OR instrument_id = ANY($1))
    GROUP BY instrument_id
) sub
ORDER BY instrument_id, range_from
```

### Read-time carry-forward

Because only real bars are stored, valuation carries the last close forward over
the days between them. The fill is derived from (bars, coverage) rather than
stored, so there is no third thing to keep in step with them.

The valuation query joins `date_series` to `price_coverage` to build the covered
grid, left-joins real bars onto it, and forward-fills with the two-window
gaps-and-islands idiom -- PostgreSQL has no `IGNORE NULLS`. Partitioning by span
bounds the carry-forward: it cannot cross a span boundary, so a delisted
instrument stops at the end of its coverage rather than holding its final close
for ever. Each span is seeded with the last bar before the window so a window
opening mid-span is not unpriced until its first bar.

A per-`(instrument, date)` lateral is the wrong shape here: `eod_prices` is a
hypertable, and a lookup whose answer lies at a data-dependent distance in the
past defeats chunk exclusion. See adr/0023-price-coverage-is-stored-not-inferred.md.

---

## Component 3: Gap Analysis (`PriceGaps`)

### Purpose

For each instrument, compute the date ranges that are **needed** (from Component 1) but **not yet cached** (from Component 2). The result is the set of date ranges that must be fetched.

### Behaviour

1. Call `HeldRanges` to get needed ranges.
2. Call `PriceCoverage` to get what has been answered for (filtered to instruments from step 1).
3. For each instrument, compute the set difference using `SubtractRanges` (Go utility in `server/db/daterange.go`).
4. Return the resulting gap ranges.

A range answered with no bars is not a gap. The fetcher additionally subtracts each plugin's own coverage before asking it, and records the negative answers it used to drop: `ErrNoData`, and any part of a gap outside the plugin's `max_history_days`. Without that, a delisted or pre-IPO range is rediscovered and refetched on every cycle for ever.

---

## Component 4: Request Optimiser

**Deferred.** This component will be implemented alongside price plugins, which will provide the `max_request_days` constraint and instrument-to-plugin matching logic needed for request optimisation.

---

## Range Utilities

`server/db/daterange.go` provides:

- `MergeRanges(ranges []DateRange) []DateRange` -- merge overlapping/adjacent ranges
- `SubtractRanges(needed, cached []DateRange) []DateRange` -- interval subtraction

These are independently unit-testable without a database.

---

## Testing considerations

Each component should be independently testable:

- **Range Utilities:** Table-driven unit tests for `MergeRanges` and `SubtractRanges`. No database required.
- **Holdings Calculator:** Insert a known set of transactions (buy, sell, buy again), verify the output ranges match expectations. Test edge cases: position goes to zero and reopens the same day; position never closes; transactions with NULL instrument_id are excluded.
- **Coverage Inventory:** Insert known spans and verify they merge correctly. Test: abutting and overlapping spans merging, a one-day hole staying unmerged, a span with no bars in it surviving, per-plugin spans staying separate, filter by instrument_id.
- **Gap Analysis:** Combine known holdings and known coverage, verify the gaps are correct. Test: fully covered (no gaps), no coverage at all (gaps = holdings), partial overlap, and a range covered with no bars producing no gap.
- **Carry-forward:** Verify the read-time fill spans non-trading days, stops at `covered_before`, does not bleed between two disjoint covered periods, and seeds a window that opens mid-span from the bar before it.
- **Containment invariant:** After every write path, assert no `eod_prices` row lies outside its instrument's coverage.

---

## FX Pairs as Instruments

FX rates are stored in `eod_prices` using synthetic FX pair instruments with
`asset_class = 'FX'` and identifier type `FX_PAIR` (value like `EURUSD`).
The `close` column stores the exchange rate (how many USD per 1 unit of base
currency). An FX pair is just another instrument with prices, so
`PriceCoverage`, `UpsertPrices`, and the range utilities work without
modification for FX data. See `display-currency.md` for the full design and
adr/0006-fx-as-synthetic-instruments.md for the rationale.

---

## Component 5: FX Gap Analysis (`FXGaps`)

### Purpose

Compute the date ranges for which FX rates are **needed** (because non-USD
instruments are held) but **not yet cached** in `eod_prices`. Unlike
`HeldRanges`, FX pairs have no transactions -- the needed ranges are derived
from when instruments in foreign currencies are held.

### Interface

```go
FXGaps(ctx context.Context, opts HeldRangesOpts) ([]InstrumentDateRanges, error)
```

### Behaviour

1. Call `HeldRanges(ctx, opts)` to get instrument held ranges.
2. For each held instrument, look up `instruments.currency`.
3. For each currency C where C != `"USD"`, look up the corresponding FX pair
   instrument ID (by querying `instrument_identifiers` for type `FX_PAIR`
   and value `CUSD`).
4. Compute the union of held ranges across all instruments sharing currency C.
   This is the "needed" range for the C/USD FX pair instrument. Use
   `MergeRanges` to consolidate overlapping ranges.
5. Call `PriceCoverage(ctx, fxInstrumentIDs)` to get existing FX rate coverage.
6. For each FX pair instrument, call `SubtractRanges(needed, cached)` to
   compute gaps.
7. Return the resulting gaps as `[]InstrumentDateRanges` with FX pair
   instrument IDs.

### Worker integration

The worker's `runCycle` is extended to call `FXGaps` after `PriceGaps` and
process the resulting gaps through the same plugin loop. FX instruments have
`asset_class = 'FX'`, so only plugins whose `AcceptableAssetClasses` includes
`'FX'` will handle them.

### Plugin extension: Massive

The Massive price plugin is extended to fetch FX data:

- `AcceptableAssetClasses()` adds `'FX'` alongside `STOCK`, `ETF`, `OPTION`.
- `SupportedIdentifierTypes()` adds `'FX_PAIR'` alongside `TICKER`, `OCC`.
- `tickerForAssetClass` handles asset class `FX` by formatting the `FX_PAIR`
  identifier value with a `C:` prefix (e.g. `C:EURUSD`), matching the
  Polygon.io forex ticker convention.
- `AcceptableCurrencies()` remains `{"USD": true}`. FX instruments have
  `currency = 'USD'` (the quote currency), so they pass this filter.

The same `DailyBars` endpoint is used -- Polygon.io returns OHLCV data for
forex pairs in the same format as equities.

### Testing considerations

- **FX gap detection:** Insert transactions for instruments with different
  currencies (e.g. one EUR, one GBP, one USD). Verify `FXGaps` returns gap
  ranges for EUR/USD and GBP/USD FX pair instruments covering the held
  periods, and no gap for USD (no FX pair needed).
- **Coverage subtraction:** Insert some `eod_prices` rows for an FX pair
  instrument, verify gaps are reduced accordingly.
- **No foreign currencies:** When all held instruments are USD, `FXGaps`
  should return an empty slice.
- **Multiple instruments same currency:** Two EUR instruments held in
  overlapping periods should produce a single merged range for EUR/USD.

---

## Out of scope

- Actual API fetching / HTTP calls to data providers.
- Trading calendar / business day logic; the system works in calendar days (see adr/0007-calendar-day-valuation.md).
- User interface.
- Authentication or multi-tenancy.
- Provider selection logic (choosing *which* provider to use for a given instrument). This will be handled by price plugins.
