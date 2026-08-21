---
status: open
title: An instrument accumulates what resolution learns
milestone: M17
dependencies: [0140]
---

When resolution reaches an instrument that already exists, what it learned
should be added to it.

## Motivation

`EnsureInstrument` looks up every identifier it was given, and when exactly one
existing instrument matches it calls `updateInstrumentOnMatch`, which sets
`underlying_id` and the option fields and nothing else. It writes no
`asset_class`, `exchange_mic`, `currency`, `name`, `cik` or `sic_code`, and it
inserts none of the identifiers it was passed.

So an instrument known by ISIN alone, reached by a plugin returning that ISIN
plus a ticker, an exchange and a name, keeps the ISIN and discards the rest.
Every later import repeats it. The instrument never accumulates, and a null
column is null for good -- which is the other half of why exchanges stay blank
(0129 is the first half).

## Scope

Fill columns that are null and insert identifiers the instrument does not have.
Do not overwrite a value already present: adr/0004 makes the identifier the
source of truth for an existing instrument, and that stands.

"What resolution learns" is narrower than what it was passed. An identifier is
inserted when it is authoritatively corroborated with a name the instrument
already holds -- the same test 0140 applies at the merge site, asked here at the
second call site. A value that arrived in the same set without any single result
tying it to a name on the row is not something this instrument learned, and
writing it would assert an association nobody made. Columns are unaffected: they
are metadata, and metadata has never merged anything.

The current behaviour has a reason attached and it needs respecting rather than
reverting. The comment ties writing no identifier to leaving each name's
`valid_from` where it was, because moving them used to disarm the retroactive
option-split guard. That is about moving names that exist, not about inserting
names that are absent -- an inserted row takes the resolution's own vintage, per
adr/0055. Prove it with a test against that guard rather than reasoning about it.
