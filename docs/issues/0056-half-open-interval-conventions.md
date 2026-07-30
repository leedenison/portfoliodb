---
status: closed
title: Align date-interval conventions across the API on half-open ranges
---

Make every date interval on the wire half-open `[from, to)`.

## Motivation

adr/0007-calendar-day-valuation.md settles the convention as half-open with
midnight-UTC values, matching PostgreSQL's `daterange` default, and the internal
range utilities follow it. The API does not, consistently:

- Half-open: `DateRange`, `ImportCoverage`.
- Closed: `ListPricesRequest`, `ImportCorporateEventCoverage`, and the prose
  describing `UpsertTxsRequest`'s replace window.

Two conventions with no marker distinguishing them is an off-by-one waiting to
happen, most likely at a boundary date where an interval is meant to abut the
next one.

## Design

Convert the closed cases to half-open and document the convention on each field.
Pre-release, so no compatibility shim is needed. Check the corresponding db-layer
queries at the same time -- the bug this prevents is a mismatch between the wire
convention and the SQL comparison operator.

See docs/spec/bitemporality.md.
