---
status: open
title: Report fees and charges to the user by period
dependencies: [0040]
---

Show the user what their holdings cost them to run: a periodic tally of `EXPENSE`
postings, grouped by period and broker.

## Motivation

Nothing surfaces charges to the user today. Once trades carry their commission as
its own posting, the data to answer "what did I pay this year" is already there and
needs only aggregating. Costs are one of the few things about a portfolio a user can
actually control, so this is worth its own surface rather than a footnote on
performance.

## Scope

All postings with `account_type = EXPENSE`, not just trade commission. That type
covers commissions, levies, custody and service charges, and taxes
(proto/api/v1/api.proto), and the recurring platform and custody charges are the
ones that compound against long-run returns. Scoping this to commission alone would
miss the point of the report.

## Design

- A plain aggregate over postings: sum by period, broker, account and commodity.
  The currency comes from the posting's instrument, so no name parsing is needed.
- Portfolio-scoped and presented under **Analysis** in
  docs/spec/information-architecture.md, alongside the other breakdowns of a
  portfolio. It is not an admin surface; 0039 is the admin view of the same ledger
  and answers a different question.
- Expressing the total as a percentage of portfolio value is what makes the number
  interpretable, but it needs a defensible denominator (average value over the
  period, not closing value). Worth doing, worth doing deliberately.

## Completeness has to be visible

The tally is only as complete as the converters. Where a broker nets commission into
a cash total and 0040 has not yet covered it, the charge is either conflated into
consideration or sitting in `Imbalance`, and the report will under-report. A number
presented without that caveat reads as authoritative when it is not.

Carry a caveat driven by the 0039 aggregate -- charges for the period, plus the
count and value of groups in it with an unresolved imbalance. That is what makes it
shippable before every converter is complete, and it reuses the imbalance query
rather than duplicating it.

## Sequencing

Depends on 0040 for the same reason: before the converters emit their expense legs,
the report is systematically wrong rather than merely incomplete.
