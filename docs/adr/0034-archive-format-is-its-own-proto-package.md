# The archive format is its own proto package

The archive schema lives in `proto/archive/v1/`, separate from `proto/api/v1/`,
and is serialised as protojson. It is the single import and export format,
replacing the standard transaction CSV, the price CSV, the instrument JSON and
the corporate event JSON.

Three reasons, in order of weight.

**Lifetime.** The API is deliberately unstable while the project is pre-release,
and can be because client and server ship together. An archive is written today
and read by a server that does not exist yet, so it cannot be. Binding the file
to the API schema couples a long-lived artefact to one that is free to break,
and turns every future proto change into a question about old exports.

**Structure.** The API `Instrument` nests `underlying` and joins `exchange_info`
so the SPA need not fetch them separately. A file wants neither: shared
underlyings become references rather than duplicated subtrees, and join results
do not belong in an archive at all. One message cannot serve both without making
one of them worse, which is why the current instrument JSON is hand-written
instead of serialising the message it is given.

**Drift.** That hand-written serialiser silently omits `cik`, `sic_code` and the
validity interval. A hand-written format drifts from the schema quietly and the
loss surfaces only at restore; a schema-derived one drifts loudly, in generated
types.

## Considered options

- **The API messages as-is** -- rejected on lifetime and structure.
- **Hand-written JSON**, as instruments and corporate events do today --
  rejected on drift, and it duplicates enum name mapping per format.
- **Binary protobuf or prototext** -- rejected: neither is readable without the
  schema, and there is no browser parser for the text format.
- **CSV** -- rejected: it cannot carry a graph, so every nested or repeated
  thing becomes comment metadata with precedence rules over it.

## Consequences

Encoding decisions that follow, and must be made once rather than per format:
emit proto field names so keys stay snake_case; unprefix `AssetClass` to match
`IdentifierType`, so protojson writes `STOCK` rather than `ASSET_CLASS_STOCK`;
mark a field `optional` wherever absent differs from zero, since an empty
`unit_price` is not a price of zero; choose the unknown-field policy explicitly
rather than inheriting a library default, because it decides whether an older
server can read a newer archive. A `format_version` in the envelope is the
escape hatch if the schema ever stops fitting.

The controlled vocabularies both packages need are shared rather than
re-declared: they moved to `proto/type/v1/` so the archive can import them
without importing `api/v1`. See
adr/0038-controlled-vocabularies-are-shared.md.

Export and import message pairs such as `ExportPriceRow` and `ImportPriceRow`
collapse into one archive message carried in both directions, so this is a net
reduction in message count rather than an addition.

Broker-specific files -- Fidelity CSV, IBKR OFX, Schwab CSV -- are unaffected.
They are the broker's own output, and converters read them into archive messages
directly; only the standard formats PortfolioDB itself defines are replaced.
