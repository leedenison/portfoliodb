---
status: open
title: Split holding declarations into pads and checked assertions
---

Allow more than one declaration per holding and distinguish declarations that
seed an opening balance from declarations that are verified against computed
holdings.

## Motivation

`holding_declarations` plus the INITIALIZE synthetic transaction is beancount's
`pad` directive: a user states a quantity at a date and the system generates a
plug transaction to make it true. That is the right design and it is well
specified in docs/spec/fixed-point.md and ADR 0011.

But in beancount `pad` and `balance` are a pair, and the safety comes from
`balance`. A pad is true by construction -- it can never catch an error. A
balance assertion is checked, and fails when the computed position disagrees
with what the user says they hold.

The schema currently forecloses assertions:

```sql
UNIQUE (user_id, broker, account, instrument_id)   -- one declaration, ever
```

A user can state "I held 500 shares on 2021-01-01" but cannot state "and 500 on
2022-12-31, and 650 on 2023-12-31". Those later statements are precisely the
ones that catch a misparsed broker CSV, a missed transfer, or a converter that
silently drops a row.

## Inspiration

Beancount's `pad` and `balance` directives. `pad` establishes an opening
position against `Equity:Opening-Balances`; `balance` verifies a position at a
date and fails the run when it does not reconcile.

## Design

- Widen the unique key to `(user_id, broker, account, instrument_id,
  as_of_date)`.
- Add a `kind` discriminator: `PAD` or `ASSERT`.
- The earliest declaration for a holding is the pad and generates the
  INITIALIZE transaction, as today. Later declarations are assertions and
  generate nothing.
- A verification pass compares the computed holding at `as_of_date` to
  `declared_qty` and surfaces mismatches, following the existing patterns for
  data problems (`validation_errors`, `identification_errors`,
  `unhandled_corporate_events`).
- Re-verify after any ingestion or recompute that could change historical
  quantities, including split recompute.

## Which share count a declaration is in

`declared_qty` has no stated denomination. The spec says only "the number of
units the user held at `as_of_date`", while the computed side of the comparison
is `SUM(t.split_adjusted_quantity)` -- today's share count. For any instrument
that has split since `as_of_date` the two are in different units, so a correct
portfolio fails the assertion by the split factor.

That has to be settled before the verification pass is worth building, or it
fires on every split and buries the errors it exists to find. A user reading
"500 shares on 2021-01-01" off an old statement means the share count current
then; a user reading today's holdings screen means today's. Both are
reasonable, so the declaration needs to say which -- a `share_count_basis`
alongside `as_of_date`, defaulting to `as_of_date`, the same question answered
for txs and prices in docs/spec/bitemporality.md.

Once denominated consistently, the assertion is the only thing in the system
that detects a transaction stored on the wrong share count basis: a
double-adjusted holding is wrong by exactly the split factor.

The API, recalc and UI plumbing already exist in
server/service/api/declarations.go and server/service/api/recalc.go.

## Note on exactness

`declared_qty` is `NUMERIC` but computed holdings sum `DOUBLE PRECISION`
columns, so the comparison crosses a float boundary. 0042 removes that. This
issue does not have to wait: an interim tolerance comparable to `qty_is_zero`
is acceptable, and tightens to an exact comparison once 0042 lands.
