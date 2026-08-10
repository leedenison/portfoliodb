---
status: closed
title: Replace TxType with a declared and resolved pair
milestone: M15
dependencies: [0092]
---

Replace `TxType` with `broker_tx_type` (what the source said, as a set),
`resolved_tx_type` (what grouping concluded) and `asset_class_hint`.

## Motivation

adr/0044-tx-type-is-declared-and-resolved.md and
adr/0045-tx-type-does-not-encode-asset-class.md set out why. In short, `TxType` is
an OFX cartesian product whose dominant factor -- buy or sell, crossed with six
asset classes -- duplicates what instrument resolution establishes, while the
factor that drives durable behaviour is barely expressed. And no source is always
specific: Fidelity's `Cash In` is either a switch's sale proceeds or money from
another account, and `asTradeCashLeg` currently guesses between them from a
grouping decision taken in the converter, which is exactly what
adr/0041-server-owns-transaction-grouping.md moves to the server. A single-valued
type gives the converter no way to hand that decision over, so this blocks 0097.

## The vocabulary

Leaves are the specificity the system acts on; internal nodes are what a less
specific source says, and are legal values.

| Value | Parent | Means | What turns on it |
|---|---|---|---|
| `TRADE` | -- | a commodity exchanged for consideration at a price | the group has a price-converted leg |
| `TRADE_ASSET` | `TRADE` | the acquired or disposed leg | weight converts at `unit_price` (replaces `exchangeTypes`) |
| `TRADE_CASH` | `TRADE` | the consideration leg | weight is its own amount, unconverted |
| `INCOME` | -- | value accruing without a disposal | counter leg is `ACCOUNT_TYPE_INCOME` |
| `DIVIDEND` | `INCOME` | distribution from an equity holding | reconcilable against `cash_dividends` (0087) |
| `INTEREST` | `INCOME` | interest received | nothing yet; see below |
| `RETURN_OF_CAPITAL` | `INCOME` | return of principal, not earnings | reduces cost basis (0031) |
| `EXPENSE` | -- | cost borne by the holder | counter leg is `ACCOUNT_TYPE_EXPENSE` |
| `TRANSACTION_COST` | `EXPENSE` | commission, levy, stamp duty, FX charge | belongs in cost basis (0031) |
| `HOLDING_COST` | `EXPENSE` | custody, platform and account fees | not basis |
| `FINANCING_COST` | `EXPENSE` | margin interest | not basis |
| `TRANSFER` | -- | a commodity moving without a change in what is held | residual routes to `TRANSFER_CLEARING` |
| `TRANSFER_INTERNAL` | `TRANSFER` | the other side is another account of this user | a match exists to be found |
| `TRANSFER_EXTERNAL` | `TRANSFER` | the other side is outside the user's holdings | no match will come; counter leg is `EQUITY` |

Plus the ambiguity value `resolved_tx_type` takes when nothing narrows a
cross-branch set, distinct from `TX_TYPE_UNSPECIFIED`.

The expense leaves are cut by how the cost is treated, not by what brokers call
the fee, so a source that says only "fee" says `EXPENSE` and that is a meaningful
answer. `INTEREST` is the one leaf nothing consumes today; it is included because
`portfoliodb.type.v1` never removes or renames a value, so pre-release is the only
window in which adding it is free (adr/0038-controlled-vocabularies-are-shared.md).

Sample mappings. Fidelity: `Buy` to `{TRADE_ASSET}`, `Cash Out For Buy` to
`{TRADE_CASH}`, `Cash In` to `{TRADE_CASH, TRANSFER}`, `Cash In Lump Sum` to
`{TRANSFER}`, dealing fee and PTM levy and stamp duty and FX charge to
`{TRANSACTION_COST}`. OFX: `BUYSTOCK` to `{TRADE_ASSET}` with the asset class
moving to the hint, bare `INCOME` to `{INCOME}`, `INVEXPENSE` to `{EXPENSE}`,
`MARGININTEREST` to `{FINANCING_COST}`, `JRNLFUND` and `JRNLSEC` both to
`{TRANSFER}`.

### Omitted deliberately

- `CLOSUREOPT` -- subsumed by `TRADE_ASSET` at a `unit_price` of zero, which
  `archive/v1/txs.proto` already relies on for an option expiring worthless.
- `JRNLFUND` and `JRNLSEC` -- collapse into `TRANSFER`. The cash-versus-security
  distinction is the instrument's, and `exchangeTypes` and `residual.transferTypes`
  already treat the two identically.
- `REINVEST` -- a compressed two-event group. The converter emits `TRADE_ASSET`
  plus `DIVIDEND` instead; `reinvestIncomeLeg` in `client/lib/csv/postings.ts`
  already performs that split, so the machinery moves earlier rather than being
  written.
- Per-fee-name leaves (`COMMISSION`, `STAMP_DUTY`, `PTM_LEVY`, `FX_CHARGE`) and
  `DISTRIBUTION` -- specificity nothing consumes.
- `DEPOSIT` and `WITHDRAWAL` -- direction is the sign of `quantity`.
- `FX` -- an FX trade is `TRADE_ASSET` plus `TRADE_CASH` with both legs in
  currencies; adr/0006-fx-as-synthetic-instruments.md already makes FX pairs
  instruments.
- `ADJUSTMENT` -- a reversal is the sign, and nothing behaves differently.
- `INITIALIZE` -- already `synthetic_purpose`
  (adr/0011-synthetic-initialize-transactions.md).
- A root value meaning "a transaction and nothing more" -- an unknown type is
  rejected, and a cross-branch set is the honest way to say "one of these".

## Scope

**Corporate actions are filtered by the converter**, and the server rejects a row
whose type it does not know. This costs nothing now: no converter emits `SPLIT`, so
`TxTypeStored` guards against something nothing produces and deletes with the rest.
The deliberate loss is that the server never sees a broker's own split row, which
could otherwise corroborate the plugin-sourced data in
adr/0005-corporate-events-design.md and
adr/0028-cumulative-split-factor-is-an-exact-rational.md.

**The hierarchy is written twice**, once in Go and once in TypeScript, rather than
carried in proto options and read by reflection. Guard the drift with a test in
each language asserting every value in the generated enum descriptor appears
exactly once in the parent map, plus a golden fixture of the flattened tree both
languages check against.

**Validation:** `min_items = 1` on the set so an empty list is not a second
spelling of "nothing known"; an antichain check, since `{TRANSFER,
TRANSFER_INTERNAL}` is meaningless; and the weight-neutrality check from
adr/0046-declared-ambiguity-is-bounded-by-weight-neutrality.md, which runs the
weight rule once per candidate and rejects a set whose members disagree.

**Predicates.** Expose `mustBe` and `mayBe` and no bare equality, so no call site
can inherit a default that silently drops rows from a report or double-counts them.

**Consumers to convert.** `residual.transferTypes`, `balance.exchangeTypes`,
`postings.ts COUNTER_ACCOUNT_TYPE`, `TxTypeToAssetClass`,
`TxTypeToInstrumentKind`, `AssetClassToTxTypeStrings`, `AssetClassToTxTypesMap`,
`IsAssetClassCompatible`, `TxTypeStored`, `TX_TYPE_LABEL`, and the three source
mappings in `client/lib/csv/converters/fidelity-csv.ts`,
`extension/src/brokers/fidelity-json.ts` and `client/lib/ofx/parser.ts`. Most of
the ~700 references across the tree are test fixtures constructing a posting, and
the generated enums make the compiler find them in both languages.

No data migration: pre-release, so the existing migration is edited rather than
added to.

## Sequencing

An ambiguous Fidelity `Cash In` arrives today as `JRNLFUND` and routes its residual
to `TRANSFER_CLEARING`. Under the every-candidate rule it routes to `IMBALANCE`
until grouping resolves it, which is more correct but moves the imbalance figures
until 0097 lands. Land the two together, or expect the reported imbalance to rise
in between.

## Spec passages to update when this lands

- `docs/spec/ofx-tx-types.md` -- the whole file; replaced by a `tx-types.md`
  describing the hierarchy, the set and the resolution rule.
- `docs/spec/postings.md` L192-199 -- the weight table, rekeyed on the new type.
- `docs/spec/identifiers.md` L96-103 -- the TxType-derived security type hint.
- `docs/spec/portfoliodb-spec.md` L70-77 and L165 -- the resolution flow diagram
  and the security type hint paragraph.
- `docs/spec/archive-format.md` L159 -- the `AccountType`/`TxType` name collision.

Closed. Landed as three PRs: the ignore rules rekeyed on the instrument's asset
class (with `TxTypeStored` deleted), the vocabulary swap itself, and the spec
updates. Two decisions taken in flight: the ambiguity value is named `AMBIGUOUS`
and modelled as the tree root, because `AssetClass` already claims `UNKNOWN` in
package scope and a rooted tree makes common-ancestor resolution one rule; and a
posting with no stated `asset_class_hint` routes to the security plugins, because
leaving both gates open let the cash plugin resolve an unidentifiable security to
its trading currency silently. Landed without 0097, so the predicted interim rise
in reported imbalance (unpaired Fidelity `Cash In` resolving `AMBIGUOUS`) is in
effect.
