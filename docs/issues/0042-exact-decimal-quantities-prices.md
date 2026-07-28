---
status: open
title: Use exact decimals for quantities and prices
milestone: M12
---

Move the `txs` quantity and price columns from `DOUBLE PRECISION` to `NUMERIC`
and carry exact decimals through the Go layer and the protobuf API.

## Motivation

Money and share quantities are decimal, not binary floating point. Four
columns on `txs` are currently `DOUBLE PRECISION`:

- `quantity`
- `split_adjusted_quantity`
- `unit_price`
- `split_adjusted_unit_price`

They are the only exception in the schema. `eod_prices` OHLC,
`instruments.strike`, `instruments.contract_multiplier`, `stock_splits`,
`cash_dividends`, `inflation_indices` and `holding_declarations.declared_qty`
are all `NUMERIC` already.

The symptom is visible in 001_initial.sql. `qty_is_zero` exists because summing
float buys and sells does not land on zero:

```sql
CREATE FUNCTION qty_is_zero(q double precision) RETURNS boolean ...
    SELECT q IS NULL OR ABS(q) < 1e-9
```

That epsilon answers "is this position closed" in holdings.go, but the same
residual flows into valuation as `quantity * price`, where there is no guard.
And `holding_declarations.declared_qty` is `NUMERIC` while the computed holding
it is compared against is not, so reconciliation already crosses a
float/exact boundary -- which matters if declarations become checked assertions
rather than only pads.

## Blocks

0041. A balance constraint needs an exact `SUM(...) = 0`. With floats it would
need an epsilon, which defeats the purpose: a genuine imbalance below the
tolerance passes silently, while a legitimate group can still fail as the
number of legs grows.

## Scope: three layers, not one

1. **Postgres** -- column types, `qty_is_zero`, `split_factor_at`.
2. **Go** -- there is no decimal dependency today; `go.mod` has `lib/pq` and
   nothing else relevant.
3. **Wire and clients** -- proto and TypeScript.

The API is already lossy for prices independently of `txs`: `eod_prices` stores
`NUMERIC` but api.proto exposes `double open/high/low/close`, and `double
quantity`, `double unit_price`, `double strike`, `double contract_multiplier`.
TypeScript `number` is float64, so the browser is lossy too. This is not only a
`txs` problem.

## Design

**Postgres.** `NUMERIC` for the four columns. `qty_is_zero` becomes `q = 0`, or
is dropped entirely.

**Go.** `shopspring/decimal` implements `sql.Scanner` and `driver.Valuer`, so
it works with `lib/pq` without changing drivers.

**Wire.** A protobuf `double` cannot carry an exact decimal. Use canonical
decimal strings: `google.type.Decimal` is already in the buf module cache, and
a plain `string` field is equivalent and simpler. Client-side arithmetic needs
a decimal library (decimal.js, big.js); display-only paths can parse the string
directly.

## Sub-problem: split_factor_at

`split_factor_at()` returns `DOUBLE PRECISION` and computes the cumulative
factor as `exp(sum(ln(...)))`. Multiplying a `NUMERIC` quantity by a double
factor returns to floating point, so this needs deciding as part of the work.
Options: compute the product in Go; use a numeric custom aggregate or a
recursive CTE in Postgres; or store the cumulative factor per instrument. The
existing comment on the function documents why exp/ln was acceptable for
realistic split chains -- that reasoning holds for the factor itself, but not
once the result feeds an exact balance check.

## Note

Pre-release, so there is no migration or back-compat burden (CLAUDE.md). The
change is wide but mechanical, and it will never be cheaper than now.
