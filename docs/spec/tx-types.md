# Transaction Types

A posting's transaction type says what kind of economic event it is a leg of.
It is carried as two fields: `broker_tx_type`, the set of candidate types the
source declared, and `resolved_tx_type`, the single value the server derived
from it, which is what every consumer reads. An `asset_class_hint` beside them
carries the class the source stated; the type itself says nothing about asset
class, and direction is the sign of `quantity`. See
adr/0044-tx-type-is-declared-and-resolved.md and
adr/0045-tx-type-does-not-encode-asset-class.md.

## The vocabulary

The types form a tree. A leaf is the specificity the system acts on; an internal
node is what a less specific source says, and both are legal values.

| Value | Parent | Means | What turns on it |
|---|---|---|---|
| `TRADE` | -- | a commodity exchanged for consideration at a price | the group has a price-converted leg |
| `TRADE_ASSET` | `TRADE` | the acquired or disposed leg | weight converts at `unit_price` |
| `TRADE_CASH` | `TRADE` | the consideration leg | weight is its own amount, unconverted |
| `INCOME` | -- | value accruing without a disposal | counter leg is `ACCOUNT_TYPE_INCOME` |
| `DIVIDEND` | `INCOME` | distribution from an equity holding | reconcilable against `cash_dividends` |
| `INTEREST` | `INCOME` | interest received | nothing yet |
| `RETURN_OF_CAPITAL` | `INCOME` | return of principal, not earnings | reduces cost basis |
| `EXPENSE` | -- | cost borne by the holder | counter leg is `ACCOUNT_TYPE_EXPENSE` |
| `TRANSACTION_COST` | `EXPENSE` | commission, levy, stamp duty, FX charge | belongs in cost basis |
| `HOLDING_COST` | `EXPENSE` | custody, platform and account fees | not basis |
| `FINANCING_COST` | `EXPENSE` | margin interest | not basis |
| `TRANSFER` | -- | a commodity moving without a change in what is held | residual routes to `TRANSFER_CLEARING` |
| `TRANSFER_INTERNAL` | `TRANSFER` | the other side is another account of this user | a match exists to be found |
| `TRANSFER_EXTERNAL` | `TRANSFER` | the other side is outside the user's holdings | no match will come |

The expense leaves are cut by how the cost is treated, not by what brokers call
the fee, so a source that says only "fee" says `EXPENSE` and that is a meaningful
answer.

`AMBIGUOUS` is the root of the tree and the value `resolved_tx_type` takes when
nothing narrows a cross-branch set. It is distinct from `TX_TYPE_UNSPECIFIED`
("the field was not set") and is never legal in a declared set, where a
cross-branch set is the honest way to say "one of these".

The hierarchy is written twice, in `server/txtype` (Go) and `client/lib/tx-type.ts`
(TypeScript). Both are checked against the golden fixture
`server/txtype/testdata/tree.json`, and each language asserts every value in its
generated enum appears exactly once in its parent map, so the two spellings
cannot drift.

## The declared set

A source asserts the most specific node it can defend: a singleton is a fully
specific assertion, an internal node covers its subtree, and a larger set is a
declared ambiguity the source could not narrow -- Fidelity's `Cash In` is
`{TRADE_CASH, TRANSFER}`, because the same row type reports both a switch's sale
proceeds and money arriving from another account.

A declared set is valid when it is non-empty, has no duplicates, names no
`AMBIGUOUS` or unspecified member, is an **antichain** (no member an ancestor of
another -- `{TRANSFER, TRANSFER_INTERNAL}` says nothing `{TRANSFER}` does not),
and is **weight-neutral**: every member must yield the same weight in the same
commodity for the posting's own quantity, price and currencies, checked at
ingest by running the weight rule once per candidate. In practice only a priced
security row can diverge, which is the case where the source has already
answered the question. See
adr/0046-declared-ambiguity-is-bounded-by-weight-neutrality.md.

A row whose type the converter does not recognise is a parse error, and the
server rejects a type it does not know. Corporate-action rows (a broker's own
split row) are filtered by the converter and never reach the server.

## Resolution

`resolved_tx_type` is derived by the server, overwriting anything a client
sends, and is not carried in an archive: an import re-derives it
(adr/0043-grouping-does-not-travel-in-the-archive.md). Until server-side
grouping lands it is the nearest common ancestor of the declared set --
`INCOME` for `{DIVIDEND, INTEREST}`, `AMBIGUOUS` for a cross-branch set. When
grouping runs as precedence-ordered passes, the pass that claims a row is what
resolves it (adr/0047-grouping-runs-as-precedence-ordered-passes.md).

## Reading a type

A rule fires only if it holds for every candidate, so the vocabulary exposes two
predicates and no bare equality:

- **must be X** -- every declared candidate (or the resolved value) lies in X's
  subtree. This is the predicate behaviour keys on: a row that may be a transfer
  and may be a trade's cash leg is not treated as a transfer.
- **may be X** -- some candidate lies in X's subtree, or is an ancestor of X: a
  source that said `INCOME` may yet turn out to have meant `DIVIDEND`.

The two differ on internal nodes -- may-be includes a node's ancestors, must-be
does not -- and either as a silent default is wrong, one dropping rows from a
report and the other double-counting, which is why call sites state which
question they ask. See adr/0044-tx-type-is-declared-and-resolved.md.

What keys on the type today:

- **Weight**: a posting converts at its price only when it must be
  `TRADE_ASSET`; see [postings.md](postings.md#balancing).
- **Residual routing**: a group's residual routes to `TRANSFER_CLEARING` when
  any leg's resolved value is under `TRANSFER`, and to `IMBALANCE` otherwise;
  an unresolved `AMBIGUOUS` leg routes to `IMBALANCE` until grouping settles it.
- **Derived counter legs**: a converter emits an `ACCOUNT_TYPE_INCOME` counter
  leg for a row that must be `INCOME` and an `ACCOUNT_TYPE_EXPENSE` one for a
  row that must be `EXPENSE`.
- **The synthetic INITIALIZE pad** is `TRANSFER_EXTERNAL`: value entering from
  outside the user's holdings, with the `EQUITY` counter leg that definition
  implies (adr/0011-synthetic-initialize-transactions.md).

## Broker vocabularies

Broker and OFX type names are the converter's input, not the system's
vocabulary. A converter maps each source string to a declared set -- Fidelity's
`Buy` to `{TRADE_ASSET}`, `Dealing Fee` to `{TRANSACTION_COST}`, `Service Fee`
to `{HOLDING_COST}`, `Cash In` to `{TRADE_CASH, TRANSFER}`; OFX's `BUYSTOCK` to
`{TRADE_ASSET}` with the asset class moving to the hint, bare `INCOME` to
`{INCOME}`. A "reinvestment" row is a compressed two-event group rather than a
kind of event: the converter emits the `TRADE_ASSET` leg plus a derived
`DIVIDEND` income leg in the same group.

The mappings live in `client/lib/csv/converters/fidelity-csv.ts` (shared with
the browser extension) and `client/lib/ofx/parser.ts`.
