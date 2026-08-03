---
status: closed
title: Imbalance and unmatched-transfer reporting
milestone: M12
dependencies: [0038]
---

Report the balance of `IMBALANCE` and `TRANSFER_CLEARING` postings, grouped by
broker, account and commodity.

## Motivation

Once residuals are routed to an explicit posting type (0038), their size is a
direct measure of how lossy each broker converter is. This turns data quality
from something inferred into something observed, so the converter work in 0040
can be prioritised by evidence rather than guesswork. A large USD imbalance under
one broker says exactly where the missing fee data is.

Unmatched transfers surface the same way: a non-zero `TRANSFER_CLEARING` balance
means one side of a journal was imported and the other was not.

## Design

- Admin-visible, following the existing patterns for surfacing data problems
  (`validation_errors`, `identification_errors`,
  `unhandled_corporate_events`). If the alerting system (0066) lands first, a
  per-broker imbalance above a threshold belongs there rather than in another
  bespoke report.
- Group by `broker`, `account` and `instrument_id`; a per-broker total is the
  useful headline number. Attribution comes from the residual posting keeping the
  broker and account of the group it balances (0038), and the currency from its
  commodity, so this is a plain aggregate and needs no name parsing.
- Consider a time dimension so that a converter fix can be seen to work.

## Expect income and charges to dominate at first

Until each converter emits the income and expense legs of its single-row
dividends and charges, those events do not balance and their full value is routed
to `IMBALANCE` (see 0038). The first version of this report will therefore be
dominated by uncategorised income rather than by the missing-fee residuals it
exists to find.

That is worth designing for rather than being surprised by. Breaking the total
down by the `tx_type` of the other postings in the group separates "this broker
reports no fees" from "we do not yet categorise this broker's dividends", and
those two findings lead to different work.

## Unmatched transfers need a maturity dimension

A `TRANSFER_CLEARING` balance is expected immediately after an import and only
becomes a problem when it persists, because the second side legitimately arrives
in a later statement. Reporting the raw balance would flag every transfer ever
imported. Age the balance -- how long a side has been waiting -- so that a
recently imported transfer is quiet and a stale one is loud.

**Not delivered, and deliberately so.** Age alone does not identify an unmatched
transfer. Both sides of a completed journal are `TRANSFER_CLEARING` postings in
different broker accounts, and nothing pairs them until 0068, so a settled
transfer and one whose second side never arrived are the same shape and both are
reported. The report therefore lists every imported transfer and says so on the
page.

Inferring the pairing from balances was tried and rejected: every rule for it
misattributes as soon as an account has more than one transfer open, reporting
the wrong account and the wrong age while looking authoritative. A report that
over-reports and admits it is better than one that quietly points at the wrong
row. 0068 is the fix, and the transfers view becomes meaningful when it lands.

## Note

This is the natural stopping point if the full sequence is not pursued. Groups
plus imbalance routing plus a report gives most of the practical value of
double-entry -- reliable cash, a measurable data-quality signal, and a
structural boundary for MWR -- without touching the converters or enabling the
constraint.
