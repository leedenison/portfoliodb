---
status: closed
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
`.Equal` but neither `==` nor `reflect.DeepEqual`. Comparing one needs an
explicit option: `testutil.DecimalOpts` for go-cmp and `testutil.DecEq` for a
gomock expectation, whose default matcher is `reflect.DeepEqual`.

The premise that the server tree "uses go-cmp throughout" was wrong -- one file
imported it and there were no shared options -- so the comparer was created
alongside the first decimal types rather than threaded ahead of them.

## Sequencing

Pre-release, so there was no migration or back-compat burden (CLAUDE.md).

The four PRs proposed here did not survive contact. There is no `db.Tx` struct:
transactions cross the db boundary as `*apiv1.Tx`, so the Go and proto changes
for `txs` are one change. And `client-typecheck` gates CI, so a proto field
becoming a string breaks TypeScript in the same commit it breaks Go. The work
was sliced by proto message group instead, each slice carrying its own Go and
client fallout, plus one client-only PR that landed the decimal library while
every field was still a number:

1. Postgres: column types, `qty_is_zero`, the `mul` aggregate and
   `split_factor_at`. The scale is `NUMERIC(38, 12)`, declared on the
   `split_adjusted_*` pair in `eod_prices` as well as `txs`.
2. Go decimal foundation and the price wire.
3. Instrument identity: `strike` and `contract_multiplier`.
4. Client converters on big.js, still emitting numbers.
5. The `Tx` wire, ingestion and the client string boundary.
6. `Holding`, `InflationIndexProto`, `ResidualBalance` and docs.

One hazard worth recording: a server-computed field cannot carry a required
format pattern. `split_adjusted_quantity` is derived by the database, so an
upload leaves it unset, `""` reaches the validating interceptor and every upload
is rejected. It is `optional`. Only the e2e suite catches this, because the
constraint lives in the interceptor rather than in any unit-testable path.
