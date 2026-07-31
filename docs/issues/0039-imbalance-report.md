---
status: open
title: Imbalance and unmatched-transfer reporting
milestone: M12
dependencies: [0038]
---

Report the balance of `Imbalance:*` and `Transfers:InFlight` accounts, grouped
by broker, account and currency.

## Motivation

Once residuals are routed to an explicit account (0038), the size of that
account is a direct measure of how lossy each broker converter is. This turns
data quality from something inferred into something observed, so the CSV format
work in 0040 can be prioritised by evidence rather than guesswork. A large
`Imbalance:USD` under one broker says exactly where the missing fee data is.

Unmatched transfers surface the same way: a non-zero `Transfers:InFlight`
balance means one side of a journal was imported and the other was not.

## Design

- Admin-visible, following the existing patterns for surfacing data problems
  (`validation_errors`, `identification_errors`,
  `unhandled_corporate_events`). If the alerting system (0066) lands first, a
  per-broker imbalance above a threshold belongs there rather than in another
  bespoke report.
- Group by broker, account and currency; a per-broker total is the useful
  headline number.
- Consider a time dimension so that a converter fix can be seen to work.

## Note

This is the natural stopping point if the full sequence is not pursued. Groups
plus imbalance routing plus a report gives most of the practical value of
double-entry -- reliable cash, a measurable data-quality signal, and a
structural boundary for MWR -- without touching the CSV format or enabling the
constraint.
