---
name: protobuf
description: Protobuf and gRPC conventions for PortfolioDB (proto/) -- proto3, buf generation, package/versioning and naming, enum rules, field documentation, and protovalidate constraints. Use when adding or changing .proto definitions or buf configuration.
---

# Protobuf Style

Use buf to generate the Go and TypeScript bindings. Generated outputs go in the
directories defined in docs/layout.md (Go beside the protos under `proto/`,
TypeScript under `client/gen`, e2e TypeScript under `e2e/gen`) and are excluded from
source control via `.gitignore`. Regenerate with `make generate`.

Use proto version 3. See [Numeric values](#numeric-values) for choosing between
`string` and `double`.

## Packages and versioning

- Package names are `portfoliodb.<area>.v1` (e.g. `portfoliodb.api.v1`,
  `portfoliodb.auth.v1`, `portfoliodb.ingestion.v1`). The directory layout mirrors
  the package: `proto/<area>/v1/<area>.proto`.
- Every file sets `option go_package = ".../proto/<area>/v1;<area>v1"`.
- `v1` is the stability boundary. This project is pre-release, so evolve `v1` in
  place rather than adding migrations or `v2` -- no backwards-compatibility shims.

## Naming

- Services are PascalCase with a `Service` suffix (`ApiService`, `AuthService`,
  `IngestionService`).
- Every RPC has dedicated `<Rpc>Request` / `<Rpc>Response` messages, even when
  empty (`DeletePortfolioResponse {}`).
- Messages are PascalCase; add a `Proto` suffix only to disambiguate from a Go
  domain type of the same name (`PortfolioFilterProto`, `EODPriceProto`).
- Fields are snake_case. Enums are PascalCase.
- Reference messages across packages by their fully-qualified name
  (`portfoliodb.api.v1.Tx`).

## Numeric values

Quantities, prices and money are exact decimals and cross the wire as canonical
decimal **strings**, not `double`. A `double` cannot carry an exact decimal, and
a `NUMERIC` column exposed as one is lossy in both directions. Use plain `string`
rather than `google.type.Decimal`, which is itself only a string wrapper -- with
`optional string` where the value is nullable.

Decide with the closure rule from adr/0026-exact-decimals-bounded-by-closure.md:
a value is decimal if its expression tree, from source-recorded values, uses only
`+`, `-` and `*`. That covers everything transcribed from a broker or a provider
(quantities, prices, strikes, OHLC bars, index values) and everything summed or
multiplied from them (holdings, posting weights, residual balances, realised
gains). Past the first division or transcendental the value is an estimate, and
`double` states that honestly -- portfolio valuations, FX-converted totals, and
return metrics. Full reasoning in
adr/0027-decimal-values-cross-the-wire-as-strings.md.

A decimal `string` field carries two things a `double` did not need:

- A trailing comment naming it as a decimal and giving units, since the type no
  longer says so: `string quantity = 4; // decimal; signed, positive for buys`.
- A protovalidate pattern. These fields were unconstrained as `double`, so this
  is new coverage rather than a like-for-like replacement:
  `[(buf.validate.field).string.pattern = "^-?[0-9]+(\\.[0-9]+)?$"]`.

## Enums

The one strict rule: every enum's zero value is the enum-name-prefixed
`*_UNSPECIFIED = 0` (e.g. `ASSET_CLASS_UNSPECIFIED = 0`, `TX_TYPE_UNSPECIFIED = 0`).
Use `optional` for nullable scalar fields, and `oneof` for mutually-exclusive
payloads.

## Documentation

Prefer inline comments over separate docs. Put a leading `//` doc comment above each
service, RPC, message, and enum describing its purpose and semantics. Use trailing
inline `//` comments on individual fields to document units, format, and optionality
(e.g. `string price_date = 3; // "YYYY-MM-DD"`, `// optional; empty = all
exchanges`). Document date-string fields consistently as `YYYY-MM-DD`.

## Validation

Field constraints use protovalidate (`buf.validate`), e.g.
`[(buf.validate.field).required = true]`, `.string.min_len = 1`,
`.enum = {defined_only: true, not_in: [0]}`.

## Linting

`buf.yaml` uses `lint: use: STANDARD` with no exclusions. Run `buf lint` (via the
buf toolchain / `make`) and keep definitions STANDARD-clean.
