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
| `adjusted_close` | `numeric` | Split/dividend adjusted close (nullable) |
| `volume` | `bigint` | Trading volume (nullable) |
| `data_provider` | `text` NOT NULL | Which provider supplied this row |
| `fetched_at` | `timestamptz` NOT NULL DEFAULT now() | When the row was inserted |

**Primary key:** `(instrument_id, price_date)`

**Index:** A TimescaleDB hypertable on `price_date`.

---

## Implementation

All components are implemented as Go functions in the database abstraction layer (`server/db`). The `PriceCacheDB` interface in `server/db/db.go` defines the contract; the Postgres implementation lives in `server/db/postgres/price_cache.go`.

Date ranges use the half-open `[From, To)` convention with `time.Time` values at midnight UTC, matching PostgreSQL's `daterange` default.

### Types

```go
// DateRange is a half-open [From, To) date range. Both values are midnight UTC.
type DateRange struct {
    From time.Time // inclusive
    To   time.Time // exclusive
}

// InstrumentDateRanges groups date ranges by instrument.
type InstrumentDateRanges struct {
    InstrumentID string
    Ranges       []DateRange
}

// HeldRangesOpts controls holdings range calculation.
type HeldRangesOpts struct {
    ExtendToToday bool // extend open positions to today
    LookbackDays  int  // extend held_from backwards by N calendar days
}
```

### Interface

```go
type PriceCacheDB interface {
    HeldRanges(ctx context.Context, opts HeldRangesOpts) ([]InstrumentDateRanges, error)
    PriceCoverage(ctx context.Context, instrumentIDs []string) ([]InstrumentDateRanges, error)
    PriceGaps(ctx context.Context, opts HeldRangesOpts) ([]InstrumentDateRanges, error)
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
   - `held_to` = the date the position returns to zero, OR today + 1 day (exclusive) if `ExtendToToday` is true and the position is still open.
4. If `LookbackDays > 0`, extend each `held_from` backwards by that many **calendar** days (to support moving average or other lookback calculations).
5. Merge overlapping or adjacent ranges for the same instrument (this can happen if a position is closed and reopened quickly, and the lookback causes overlap).
6. Return the result as a slice of `InstrumentDateRanges`.

---

## Component 2: Coverage Inventory (`PriceCoverage`)

### Purpose

For each instrument present in the `eod_prices` table, return the date ranges for which we already have cached data, as maximally merged non-overlapping ranges.

### Behaviour

1. For each instrument (or specific instruments if `instrumentIDs` is non-empty), use PostgreSQL's `range_agg` to merge individual price dates into contiguous `daterange` values.
2. Extract the lower and upper bounds as plain DATE values.
3. Return as a slice of `InstrumentDateRanges`.

### SQL approach

```sql
SELECT instrument_id, lower(r) AS range_from, upper(r) AS range_to
FROM (
    SELECT instrument_id,
        unnest(range_agg(daterange(price_date, price_date + 1))) AS r
    FROM eod_prices
    WHERE ($1::uuid[] IS NULL OR instrument_id = ANY($1))
    GROUP BY instrument_id
) sub
ORDER BY instrument_id, range_from
```

---

## Component 3: Gap Analysis (`PriceGaps`)

### Purpose

For each instrument, compute the date ranges that are **needed** (from Component 1) but **not yet cached** (from Component 2). The result is the set of date ranges that must be fetched.

### Behaviour

1. Call `HeldRanges` to get needed ranges.
2. Call `PriceCoverage` to get what we have (filtered to instruments from step 1).
3. For each instrument, compute the set difference using `SubtractRanges` (Go utility in `server/db/daterange.go`).
4. Return the resulting gap ranges.

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
- **Holdings Calculator:** Insert a known set of transactions (buy, sell, buy again), verify the output ranges match expectations. Test edge cases: position goes to zero and reopens the same day; position never closes; lookback extends before first transaction; transactions with NULL instrument_id are excluded.
- **Coverage Inventory:** Insert a known set of `eod_prices` rows with deliberate gaps, verify the contiguous ranges are detected correctly. Test: single day of data, data with weekend gaps, data with a genuine multi-day gap; filter by instrument_id.
- **Gap Analysis:** Combine known holdings and known cached data, verify the gaps are correct. Test: fully cached (no gaps), no cache at all (gaps = holdings), partial overlap.

---

## Out of scope

- Actual API fetching / HTTP calls to data providers.
- Trading calendar / business day logic (we work with calendar days throughout).
- User interface.
- Authentication or multi-tenancy.
- Provider selection logic (choosing *which* provider to use for a given instrument). This will be handled by price plugins.
