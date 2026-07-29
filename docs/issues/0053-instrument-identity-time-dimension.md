---
status: open
title: Give instrument identity a time dimension
---

Make instrument identity a time-varying fact rather than current state.

## Motivation

Instrument identity is shared reference data that changes retroactively, and
nothing records what was believed before:

- `instrument_identifiers` is a flat current-state mapping. A ticker reassigned
  to a different issuer, or a reused CUSIP, silently rewrites the interpretation
  of every historical transaction that resolved through it.
- `instruments.valid_from` and `valid_to` exist and are the natural home for
  this, but no query filters on them.
- Eager merge deletes the merged-away instrument and its identifiers in one
  transaction with no audit trail, so holdings computed last month may not
  reproduce.

## Design

- Query `valid_from` / `valid_to` during resolution so an identifier resolves to
  the instrument that held it at the transaction's date, not merely to whatever
  holds it now.
- Give `instrument_identifiers` a validity interval so ticker reuse is
  representable.
- Record merges rather than deleting the loser outright.

See adr/0004-instrument-resolution-and-merge.md and
docs/spec/bitemporality.md.
