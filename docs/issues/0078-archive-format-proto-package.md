---
status: closed
title: Define the archive format in its own proto package
dependencies: [0092]
---

Define the schema every export and import speaks, in `proto/archive/v1/`,
encoded as protojson.

## Motivation

Four file formats exist today -- the transaction CSV, the price CSV, the
instrument JSON and the corporate event JSON -- each with its own serialiser,
its own enum name mapping and its own conventions for the data that does not fit
in rows. Coverage is spelled two ways for one meaning. Everything that spans
rows ends up as `#` comment metadata with precedence rules layered over it.

One schema replaces all four. See
adr/0034-archive-format-is-its-own-proto-package.md for why it is a separate
package rather than the API messages, and
adr/0035-archive-nests-by-aggregate-root.md for the shape.

## Design

Shared message types, so the same concept is defined once: an identifier triple,
a coverage interval, a decimal string, an envelope. Per-entity messages compose
them.

Three levels, with no inheritance between them:

- **File** -- `format_version`, `exported_at`, source instance. Only fields that
  cannot vary between rows.
- **Group** -- the aggregate root. Instrument for prices and corporate events,
  tx group for transactions, statement for declarations. Coverage,
  `share_count_basis`, asset class and currency live here.
- **Row** -- only what varies per row.

Encoding decisions to settle once, per the ADR's consequences: emit proto field
names so keys stay snake_case; unprefix `AssetClass` so protojson writes `STOCK`
rather than `ASSET_CLASS_STOCK`; mark fields `optional` wherever absent differs
from zero; and choose the unknown-field policy explicitly, since it decides
whether an older server can read a newer archive.

Settle the container shape here too: one protojson document, or a manifest plus
per-entity parts. It has to accommodate a streamed export of ~139,000 price rows
and a restore that reads parts in dependency order.

Existing export and import message pairs such as `ExportPriceRow` and
`ImportPriceRow` collapse into one archive message carried in both directions.

## Documentation

A new `docs/spec/archive-format.md` covering both the admin and user archives
(adr/0033-system-and-user-archives-are-separate.md). `docs/spec/csv-format.md`
shrinks as each format migrates under 0081 and 0084, and is deleted once it
documents nothing.
