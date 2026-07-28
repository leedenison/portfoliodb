---
status: open
title: Extend standard CSV with fees and net amount
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
