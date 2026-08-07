---
status: closed
title: Move the controlled vocabularies into a shared proto package
---

Move `AssetClass`, `IdentifierType`, `TxType`, `AccountType` and `Broker` out of
`proto/api/v1/api.proto` into a new `proto/type/v1/type.proto`, and unprefix
`AssetClass` on the way.

## Motivation

The archive format is defined in its own package and cannot import
`portfoliodb.api.v1` -- adr/0034 rejects binding a long-lived file to a schema
that is deliberately free to break. Without a shared home, every vocabulary the
archive needs is declared twice and needs a conversion function in each
direction in each of Go and TypeScript, which is the per-format enum mapping
adr/0034 rejected hand-written JSON for.

The names are not an API detail: `txs.type` stores `"BUYSTOCK"`,
`instrument_identifiers.identifier_type` stores `"ISIN"`,
`instruments.asset_class` stores `"STOCK"`. `AssetClass` is the odd one out --
it is the only vocabulary whose proto value names differ from what is stored,
which is why `AssetClassToStr` has to strip a prefix and `StrToAssetClass` has
to add one back. Unprefixing removes the gap.

See adr/0038-controlled-vocabularies-are-shared.md.

## Design

`portfoliodb.type.v1` carries a stability contract the rest of `proto/` does
not: values are never renumbered or removed and names never change, because an
archive written years ago spells them on disk. `portfoliodb.api.v1` keeps its
pre-release freedom to break.

`AccountType` stays prefixed. `INCOME` and `TRANSFER` collide with `TxType`, and
proto enum values share package scope rather than file scope, so moving the two
vocabularies into one package does not change that.

TypeScript member names are almost unaffected, because protobuf-es already
strips the common prefix: `AssetClass.STOCK` stays `AssetClass.STOCK`, and only
`AssetClass.UNSPECIFIED` becomes `AssetClass.ASSET_CLASS_UNSPECIFIED`, matching
how `IdentifierType` already reads.

## Linting

`buf lint` is configured `STANDARD` but nothing runs it, and the tree has 64
findings. Wire a `lint-proto` target into `make check` and make it green:

- `ENUM_VALUE_PREFIX` is excepted. Satisfying it would rename `BUYSTOCK` to
  `TX_TYPE_BUYSTOCK` and reintroduce prefix stripping everywhere -- the opposite
  of what this issue does for `AssetClass`.
- `PACKAGE_DIRECTORY_MATCH` is excepted. Satisfying it means moving
  `proto/api/v1/` to `proto/portfoliodb/api/v1/` and rewriting every proto
  import, `go_package`, generated output path and TypeScript import across the
  client, the extension and the e2e suite, for a cosmetic gain.
- The RPC naming findings are fixed: `auth.proto` reusing
  `google.protobuf.Empty` and `AuthUserResponse` across RPCs,
  `ingestion.proto` reusing `IngestionResponse`, and
  `ExportInstruments` streaming a bare `Instrument`.
