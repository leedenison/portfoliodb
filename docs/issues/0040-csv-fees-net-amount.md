---
status: open
title: Extend standard CSV with fees and net amount
milestone: M12
dependencies: [0039]
---

Add `fees` and `net_amount` columns to the standard CSV format and update the
broker converters to populate them.

## Motivation

The standard CSV cannot currently express a balanced transaction. It carries
`quantity`, an optional `unit_price`, `trading_currency` and
`settlement_currency`, but no fee and no cash total. `quantity * unit_price` is
the gross consideration; the actual cash movement is gross plus commissions and
charges. Without those, every derived cash leg is wrong by the fee and the
difference lands in `Imbalance` (0038).

This is a breaking change to the format, which is acceptable: the project is
pre-release and CLAUDE.md states that data models and APIs are not stable and
should not carry migrations or back-compat.

## Design

- `net_amount` -- signed cash movement in `settlement_currency`. Authoritative
  when present: use it directly for the cash leg rather than deriving.
- `fees` -- total commissions and charges for the row, in
  `settlement_currency`.
- Precedence: `net_amount` when supplied; otherwise
  `-(quantity * unit_price) - fees`; otherwise security leg only with the
  balance falling to `Imbalance`.
- Post `fees` to an expense account rather than folding it into the cash leg,
  so that cost basis and expenses stay separable. This matters for the lot and
  cost-basis work if that is taken up later.
- Both columns optional, so existing hand-written CSVs keep working and only
  contribute imbalance.

## Sequencing

Prioritise converter updates using the imbalance report from 0039 -- fix the
broker with the largest residual first. The Fidelity converter (0013) is the
existing worked example.

## Manual test data

The broker CSVs under `local/standard-format` predate these columns, so they
carry no fee or cash total and every one of their derived cash legs will land in
`Imbalance` until they are regenerated. Doing that is part of this work: the
scripts that produce them live in `local/scripts` and have to populate the new
columns alongside the client converters.

Whether the source data supports it varies by broker, so check before assuming a
regeneration is mechanical. The IBKR master (`local/masters/Lee-IBKR-CWSY.csv`)
has no commission column at all -- only `Amount`, the cash total with commission
already in it. Two ways round that:

- Take fees from the OFX/QFX export of the same account, which does carry
  `<COMMISSION>` per transaction alongside `UNITPRICE` and `TOTAL`.
- Derive them as `|Amount| - quantity * multiplier * unit_price`, which is what
  the residual is once the price is in the instrument's currency.

That currency qualifier matters. The IBKR export quotes `Price` in the account's
base currency while `Amount` is in the instrument's, so the conversion has to
happen before any fee is derived from the difference. `convert-ibkr.py` already
does it and documents why.
