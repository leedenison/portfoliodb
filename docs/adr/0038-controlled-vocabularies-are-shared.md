# Controlled vocabularies are shared between the API and the archive

The five controlled vocabularies -- `AssetClass`, `IdentifierType`, `TxType`,
`AccountType` and `Broker` -- live in `proto/type/v1/` rather than in
`proto/api/v1/`, and both the API and the archive format
(adr/0034-archive-format-is-its-own-proto-package.md) import them from there.

The archive cannot import `portfoliodb.api.v1`: an archive is written today and
read by a server that does not exist yet, so it cannot be bound to a schema that
is deliberately free to break while the project is pre-release. The alternative
to sharing is therefore re-declaring all five in `portfoliodb.archive.v1`, which
buys the separation at the cost of five duplicated vocabularies and ten
conversion functions across Go and TypeScript -- the same per-format enum
mapping that adr/0034 rejected hand-written JSON for. These names are not an API
detail. They are what the database stores: `txs.type` holds `"BUYSTOCK"`,
`instrument_identifiers.identifier_type` holds `"ISIN"`, `instruments.asset_class`
holds `"STOCK"`. One definition, shared, is the honest description of that.

## Consequences

`portfoliodb.type.v1` is the one part of `proto/` that is not free to break. A
value is never renumbered and never removed, and a value name never changes,
because the name is simultaneously the proto identifier, the stored string and
what an archive written years ago spells on disk. Adding a value is the only
backwards-compatible change, and even that makes a file naming it unreadable to
an older server -- which is what the archive's `format_version` exists to
report. `portfoliodb.api.v1` keeps its pre-release freedom to break.

`AssetClass` values are unprefixed on the move (`STOCK`, not
`ASSET_CLASS_STOCK`), which adr/0034 asks for so protojson writes `"STOCK"`, and
which deletes the `TrimPrefix` and `"ASSET_CLASS_"+s` juggling that stood
between the enum and the column. `AccountType` stays prefixed: `INCOME` and
`TRANSFER` collide with `TxType` in package scope, and proto enum values share
package scope rather than file scope, so co-locating the two vocabularies does
not change that.

This makes `buf lint`'s `ENUM_VALUE_PREFIX` rule permanently wrong for this
repo, and `buf.yaml` excepts it explicitly rather than leaving the tree
unlintable.

`TxType` is replaced wholesale by
[0044](0044-tx-type-is-declared-and-resolved.md) and
[0045](0045-tx-type-does-not-encode-asset-class.md), which is exactly the break
this contract forbids. It is spent deliberately and once, while the project is
pre-release and no archive naming the old values exists outside the repository.
After that the contract holds as stated, and a value added later -- a finer income
or expense leaf, say -- costs a `format_version` bump rather than being free, which
is why the vocabulary is cut at the specificity it is rather than at the smallest
set anything consumes today.

The resolved transaction type needs a value meaning "ambiguity survived", and it is
a member of the vocabulary rather than the zero value. `AssetClass` already carries
`UNKNOWN` alongside `ASSET_CLASS_UNSPECIFIED` for the same reason: "not set" and
"looked at, still unknown" are different claims, and a shared vocabulary that
conflates them cannot say which an archive meant.

