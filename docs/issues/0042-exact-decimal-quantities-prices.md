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

0041. See that issue for what a balance constraint gains from exactness.

## Scope

The boundary is set by adr/0026-exact-decimals-bounded-by-closure.md: exact
decimals up to and including the last `+`, `-` or `*` from a recorded value, and
`double` past the first division or transcendental. Applying it here:

**Postgres.** `NUMERIC` for the four columns. `qty_is_zero` becomes `q = 0` at
its three call sites (`holdings.go`, `residual_balances.go`, `valuation.go`), or
is dropped. The valuation query keeps its day-grid arithmetic on `float8` by
casting the operands -- not the result -- at the point of FX conversion; see
spec/performance.md.

**Go.** `shopspring/decimal` implements `sql.Scanner` and `driver.Valuer`, so it
works with `lib/pq` without changing drivers. It stops at the db and API layers:
the Massive and EODHD plugin clients parse JSON from providers that emit floats,
and there is nothing to gain by converting them.

**Wire.** Decimal strings, per adr/0027-decimal-values-cross-the-wire-as-strings.md.
25 of the 26 `double` fields in api.proto move; `ValuationPoint.total_value`
stays `double`. `ExportPriceRow` and `ImportPriceRow` are the clearest case,
being a round-trip pair that currently downgrades a `NUMERIC` column through a
`double` in both directions. `Instrument.strike` is the next clearest: it is
denormalised from the OCC identifier and so is a component of option identity.
Add protovalidate patterns while the fields are being touched -- none of the 26
carries a constraint today.

**Clients.** A decimal library is needed only in the CSV and OFX converters under
`client/lib/csv/` and `client/lib/ofx/`, which author facts and are shared with
the extension. Pick it here: the converters need only the four operations and
comparison, and the modules are shared with an MV3 extension where bundle size
counts, so big.js (around 8KB minified) is the smaller fit and decimal.js (around
32KB) buys arbitrary-precision functions nothing on the client uses. Display
paths get simpler, not harder: the
`parseFloat(tx.quantity.toFixed(4))` in `client/app/transactions/page.tsx` exists
to hide float artifacts and deletes outright. Chart series stay `number`.

## split_factor_at

Settled in adr/0028-cumulative-split-factor-is-an-exact-rational.md: a `mul`
aggregate over `numeric`, returning numerator and denominator separately so the
single division is deferred to the caller. The existing `exp(sum(ln(...)))`
implementation and the comment defending it both go.

The `split_adjusted_*` columns then need a **declared rounding scale**, because a
reverse `/3` has no exact decimal form. Pick a number as part of this work --
more decimal places than any broker quotes fractional shares to -- and record it
in the schema alongside the columns.

## Known costs

`decimal.Decimal` wraps a `big.Int` and an exponent, so `1.0` and `1.00` are
`.Equal` but neither `==` nor `reflect.DeepEqual`. The server tree has around 78
`float64` references in tests and uses go-cmp throughout, so a
`cmp.Comparer(decimal.Decimal.Equal)` needs threading through the test helpers,
and it should land before the types change rather than after.

## Sequencing

Pre-release, so there is no migration or back-compat burden (CLAUDE.md). The
change is wide but mechanical, and it will never be cheaper than now. Four PRs,
to stay near the 500-800 line target:

1. Postgres: column types, `qty_is_zero`, the `mul` aggregate and
   `split_factor_at`.
2. Go: the go-cmp comparer first, then decimal through the db layer, ingestion
   and balancing.
3. Proto: string fields, protovalidate patterns, regenerate.
4. Client and extension.
