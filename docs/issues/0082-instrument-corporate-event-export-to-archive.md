---
status: open
title: Move instrument and corporate event export to the archive schema
dependencies: [0078]
---

Replace the two hand-written JSON formats with the archive schema.

## Motivation

Instrument identity and corporate events already export as JSON, so the shape is
roughly right and the pressure is lower than it is for the CSV formats. What
they carry is duplication: `instrumentsToJson` and `splitsToJson` each hand-roll
their own enum name maps, their own identifier serialisation and their own
parse-error handling, and the corporate event format spells coverage as a JSON
array where the price CSV spells the same thing as a comment line.

They are also where format drift shows. `instrumentsToJson` silently omits
`cik`, `sic_code` and the validity interval, which nobody notices until a
rebuild -- the failure mode
adr/0034-archive-format-is-its-own-proto-package.md records.

## Design

Both become admin archive parts, grouped by instrument per
adr/0035-archive-nests-by-aggregate-root.md.

Retire `instrumentsToJson` / `jsonToInstruments` in client/lib/json/instruments.ts
and `splitsToJson` / `parseSplitsJson` in client/lib/json/corporate-events.ts,
along with their duplicated `IDENTIFIER_TYPE_NAME` and `IDENTIFIER_TYPE_BY_NAME`
tables, and the corporate event JSON section of docs/spec/csv-format.md.

The instrument format keeps positional references for underlyings rather than
nesting them, so a shared underlying appears once. The archive never carries
server UUIDs, and never carries `exchange_info`, which is a join result the SPA
needs and a file does not.

What the instrument part should additionally carry is 0083.
