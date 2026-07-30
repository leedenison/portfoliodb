---
status: closed
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

## Resolution

Closed without giving identity a time dimension. An identifier resolves to
whichever instrument holds it now, and a merge still deletes the loser.

Nothing supplies the valid time the design would query. No identifier plugin
returns `valid_from` or `valid_to`; they are set only through the admin
`ImportInstruments` payload and are NULL for every instrument the system
resolves for itself. Filtering resolution on them would filter on nothing.

The cost lands on the resolution path, which is the hottest code in ingestion. A
validity interval on `instrument_identifiers` means replacing the four
uniqueness indexes with a GiST exclusion constraint and a new `btree_gist`
dependency, teaching the trigger that denormalises `instruments.name` to pick a
vintage, adding an as-of argument to every identifier lookup and to
`EnsureInstrument`, and adding the date to both batch resolution caches -- plus
proto, import/export and admin UI. That is the whole path, for data no source
provides.

Nothing reads the history a merge record would keep. Holdings and valuation
follow from the current instrument set and no API reports what an instrument
used to be. The one consumer was 0054, itself since closed for the same reason.

Merge stays lossy, and that is accepted: transactions and identifiers move to
the survivor, but the loser's canonical fields and its cascaded prices, splits,
dividends and coverage rows are deleted. Those derive from external sources and
are recoverable by re-fetch.

The one time dimension identity does carry is `identity_as_of`, the point in
market time the stored identity reflects, which gates retroactive option split
adjustment. That is a single timestamp on the current identity, not a history of
it; see 0055 and adr/0017-option-identity-reflects-ex-date.md.

The reasoning is recorded in adr/0004-instrument-resolution-and-merge.md and the
resulting behaviour in spec/identifiers.md and spec/bitemporality.md.
