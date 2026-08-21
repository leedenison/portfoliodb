# Controlled vocabularies are shared between the API and the archive

The five controlled vocabularies -- `AssetClass`, `IdentifierType`, `TxType`,
`AccountType` and `Broker` -- live in `proto/type/v1/` rather than in
`proto/api/v1/`, and both the API and the archive format
([0034](0034-archive-format-is-its-own-proto-package.md)) import them from there.

The archive cannot import `portfoliodb.api.v1`: an archive is written today and
read by a server that does not exist yet, so it cannot be bound to a schema that is
deliberately free to break while the project is pre-release. The alternative to
sharing is re-declaring all five in `portfoliodb.archive.v1`, which buys the
separation at the cost of five duplicated vocabularies and ten conversion functions
across Go and TypeScript -- the same per-format enum mapping 0034 rejected
hand-written JSON for. These names are not an API detail. They are what the
database stores: `txs.type` holds `"BUYSTOCK"`,
`instrument_identifiers.identifier_type` holds `"ISIN"`, `instruments.asset_class`
holds `"STOCK"`. One definition, shared, is the honest description of that.

## Consequences

`portfoliodb.type.v1` is the one part of `proto/` that is not free to break. A value
is never renumbered and never removed, and a value name never changes, because the
name is simultaneously the proto identifier, the stored string and what an archive
written years ago spells on disk. Adding a value is the only backwards-compatible
change, and even that makes a file naming it unreadable to an older server -- which
is what the archive's `format_version` exists to report. `portfoliodb.api.v1` keeps
its pre-release freedom to break.

Because a later addition costs a `format_version` bump rather than being free, the
vocabulary is cut at the specificity it needs rather than at the smallest set
anything consumes today. The one break spent so far is `TxType`, replaced wholesale
by [0044](0044-tx-type-is-declared-and-resolved.md) and
[0045](0045-tx-type-does-not-encode-asset-class.md) while the project is pre-release
and no archive naming the old values exists outside the repository.

`AssetClass` values are unprefixed (`STOCK`, not `ASSET_CLASS_STOCK`), which 0034
asks for so protojson writes `"STOCK"`, and which deletes the `TrimPrefix` and
`"ASSET_CLASS_"+s` juggling that stood between the enum and the column.
`AccountType` stays prefixed: `INCOME` and `TRANSFER` collide with `TxType` in
package scope, and proto enum values share package scope rather than file scope.
This makes `buf lint`'s `ENUM_VALUE_PREFIX` rule permanently wrong for this repo,
and `buf.yaml` excepts it explicitly rather than leaving the tree unlintable.

A vocabulary needs a value meaning "looked at, still unknown" alongside its zero
value, because "not set" and "looked at, still unknown" are different claims and a
shared vocabulary that conflates them cannot say which an archive meant.
`AssetClass` carries `UNKNOWN` beside `ASSET_CLASS_UNSPECIFIED`, and the resolved
transaction type carries the equivalent for "ambiguity survived".
