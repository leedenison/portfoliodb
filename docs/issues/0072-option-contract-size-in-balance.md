---
status: closed
title: Weigh an option leg by its contract size when balancing
milestone: M12
dependencies: [0038]
---

`weightOf` converts a security leg at `quantity * unit_price`, which is the
consideration for a share but a hundredth of it for an option. No option trade
can balance, so its whole notional is routed to `IMBALANCE`.

## Motivation

An option is quoted per share and traded per contract. IBKR's QFX reports
`<UNITS>8`, `<UNITPRICE>20.1105585` and `<TOTAL>-16095.867048`, which reconciles
only as `8 x 20.1105585 x 100 + 7.420248` of commission. Weighing the leg without
the contract size leaves 99% of the trade behind.

On the IBKR sample export that is 391 of 659 groups and a residual of 175,279
USD; with the contract size applied it falls to 9,398, which is commission plus
the FX estimate error the export's own converter documents. The report from 0039
is unreadable until this is fixed -- the missing-fee residuals it exists to find
are two orders of magnitude smaller than the noise sitting on top of them.

## Design

- Shares per contract is `100 * contract_multiplier` for an `OPTION`, and 1 for
  everything else. The 100 is the OCC standard deliverable and belongs to the
  asset class; `contract_multiplier` records only the deviation from it, which is
  what the column already means (`1 = standard (100 shares/contract)`, per
  `server/migrations/001_initial.sql`). So there is nothing new to populate:
  every standard contract already carries the right value.
- It is a property of the instrument, not of the tx type, so it belongs on
  `balanceInstrument` alongside `isCurrency` -- the same reason that field is
  there rather than being inferred from the type.
- A multiplier of zero is treated as 1. The column is `NOT NULL DEFAULT 1` so the
  database cannot supply one, but a zero would silently weigh a whole trade to
  nothing, which is the failure this issue is about.

## Futures are not covered

A future's contract size varies per contract -- 50 for ES, 1000 for CL -- and
there is no standard for `contract_multiplier` to be a multiple of. Weighing one
needs a contract size stored per instrument, which is a field the datamodel does
not have and no plugin supplies. No future has ever been imported, so this is
recorded rather than solved: a `BUYFUTURE` leg weighs as though its contract size
were 1, exactly as it did before.
