---
status: open
title: Identifier validity intervals replace identity_as_of
milestone: M21
dependencies: [0124]
---

Move the validity of a name onto the name: `instrument_identifiers` gains a
half-open `[valid_from, valid_before)` interval and `instruments.identity_as_of`
is retired. A split then mints an OCC rather than rewriting one, so a contract's
pre-split and post-split symbols both resolve to it. See
[0055](../adr/0055-identifier-validity-is-an-interval.md) for why, including why
the global unique index on `(identifier_type, value)` cannot survive retained
history.

The work:

- Schema: the two interval columns, `btree_gist`, and a GIST exclusion
  constraint over `(identifier_type, COALESCE(domain,''), interval)` replacing
  the four partial unique indexes.
- `ApplyOptionSplit` closes the old OCC at the `ex_date` and inserts the new one
  from it, in place of delete-insert-and-advance-the-stamp.
- The pending-split query keys off the OCC row's `valid_from` and open
  `valid_before` rather than `instruments.identity_as_of`, keeping the
  `ex_date <= expiry` and `ex_date <= CURRENT_DATE` bounds, and falls back to the
  option's first trade date where `valid_from` is NULL.
- Identifier lookups filter on validity: `FindInstrumentByIdentifier`, the
  DB-only resolve path, the FX joins in valuation and the price cache,
  `SplitsByUnderlyingTicker` and identifier priority.
- `recompute_instrument_name()` filters to the open interval, which lets
  `ApplyOptionSplit` stop overriding the name it derives.
- The archive carries the interval per identifier in place of `identity_as_of`
  per instrument, and restores it.
- Mint the new name through the existing merge path rather than failing on a
  collision, so a duplicate instrument created while the split was unknown is
  absorbed instead of leaving the option pending on a rolled-back transaction
  every cycle.
- Docs: `docs/spec/bitemporality.md` records identity as current state and
  ticker reuse as not representable, and both stop being true. Mark
  [0017](../adr/0017-option-identity-reflects-ex-date.md) superseded by 0055 when
  this lands.
