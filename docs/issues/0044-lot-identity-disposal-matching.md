---
status: open
title: Lot identity and disposal matching
milestone: M19
---

Give acquisitions a lot identity and make disposals reference the lots they
reduce.

## Motivation

`unit_price` is recorded on each tx, so cost basis is derivable by replay, but
there is no lot identity and no disposal matching -- a sale does not reference
what it sold. Holdings are `SUM(quantity)` grouped by instrument, which
collapses every acquisition into a scalar.

What that scalar costs:

- **No realised gain.** Current value is computable; return is not.
- **No unrealised/realised split**, and no way to attribute a change in value
  to market movement versus contributions and withdrawals.
- **No capital gains reporting**, which needs acquisition history per
  disposal.

The information is present in the transaction data. It is discarded by the
aggregation.

## Inspiration

Beancount's inventory model, where a posting carries a cost
(`10 SYM0 {£4.50}`) and a disposal nominates the lot it reduces.

The borrowing stops at the data model. Beancount makes the gain fall out of the
requirement that the transaction balances, which works because its disposal leg
weighs at **cost**, leaving the gain as the residual. Ours weighs at **price**
(adr/0024-group-balance-is-checked-on-weight.md), so a `SELLSTOCK` group already
balances against its cash row and there is no residual for a gain to occupy. See
[Gains are derived, not posted](#gains-are-derived-not-posted).

## What opens a lot

A lot is opened or augmented by a `USER` posting whose quantity has the **same
sign as the running position**, and reduced by one whose sign opposes it.
Quantities are signed with no type-based sign flip (docs/spec/archive-format.md,
adr/0020-double-entry-postings.md), so the rule needs no enumeration over the 24
`TxType` values and covers `REINVEST`, `CLOSUREOPT`, inbound transfers, pads and
short positions alike. A classification over tx types would also get a `BUY`
that covers a short wrong, calling it an acquisition. This is the objection
adr/0022-typed-per-account-cash-flow-boundary.md raised to classifying cash flows
by tx type, in the same shape.

Lot identity is the acquiring posting's `txs.id`. Nothing new is minted.

## Where a lot's cost comes from

The acquisition's `weight` is exactly `quantity * unit_price * contract_size` in
the settlement currency, stored and exact
(adr/0029-posting-weight-is-stored.md), and the `EXPENSE` legs of its group are
the incidental acquisition costs, which have been separate postings since 0040.
So the cost is a read over the group the acquisition already belongs to.

It is stored on the lot anyway, for the reason 0029 stores weight: instrument
state moves under a posting after ingest, so a re-derived cost could disagree
with the one the lot was built on.

## Whether a lot's cost is known

Nothing needs to record this. The acquisition's counterparty leg already says
it, and reading it is the same read the cash-flow boundary does:

| Counterparty in the group | Basis                                                       |
| ------------------------- | ----------------------------------------------------------- |
| `USER` cash leg           | Known and exact.                                             |
| Part `IMBALANCE`          | Partially known -- the residual is missing from the cost.    |
| `EQUITY`                  | A pad. Value entering from before the history begins, so unknown by construction. |
| `TRANSFER_CLEARING`       | Pending. Known once the pair is matched, unknown when the far side is outside our data. |

**Unknown is a value, not a default.** `cost` is NULL where it is not known, and
the gain figures downstream report a known/unknown split rather than a number
that covers the gap. Fabricating a basis -- zero cost, or the market price on the
date -- is rejected on the grounds already recorded twice in this repository:
adr/0026-exact-decimals-bounded-by-closure.md, that encoding an estimate as an
exact decimal misrepresents its provenance; and ADR 0024's asymmetry, that a
residual left visible is attributable and fixable while converting wrongly
deletes a holding and puts cash in its place, silently. It is also the
covered/noncovered split a US 1099-B carries, which exists because
transferred-in positions with no basis are ordinary rather than exceptional.

An unknown basis is filled in, in order of authority:

1. the user declares it on the holding declaration (0075);
2. the matched counterparty supplies it (0068);
3. an explicitly user-invoked estimate from the price series, stored with its
   provenance and never applied silently.

The acquisition **date** does get a default -- the pad or transfer date -- but for
ordering only. It must not drive the 30-day rule or a long/short-term split,
because it is not an acquisition date and does not become one by being the only
date available.

Worth stating because it bounds the damage: performance needs no cost basis at
all. TWR and MWR need the market value at period start and the flows across the
boundary, which valuation and 0037 already supply. A padded portfolio has
correct returns and incomplete gains, not both broken.

## Transfers

adr/0024-group-balance-is-checked-on-weight.md rejected carrying the cost on both
legs of a securities transfer, so a `JRNLSEC` weighs in shares and its
`TRANSFER_CLEARING` counterparty holds shares rather than money. Cost therefore
cannot ride the postings, and reaches the receiving side another way:

- **Matched intra-user.** Not a disposal. The lots move along the group-to-group
  link 0068 records, keeping their acquisition dates and their costs.
- **Unmatched inbound.** Quantity known, cost unknown.
- **Unmatched outbound.** Presumed a transfer rather than a sale. The removed
  cost is recorded so that it restates when the pair arrives.

0068 is not a dependency. Without it an inbound transfer yields an unknown-basis
lot, which the model above already handles; what it gates is *coverage*. Under
Section 104 it does not matter at all, because the pool spans the person and an
intra-user transfer never touches it. Under the per-account methods it is the
difference between a basis that arrives and one that stays unknown.

## Corporate actions

Corporate actions must adjust lots, not just totals: `split_factor_at` and the
`split_adjusted_*` recompute need a lot-aware equivalent. A lot inherits the
`share_count_basis` of the acquisition that created it, so the lot-aware factor
reads that column rather than the disposal date or the query date -- see
docs/spec/bitemporality.md. A pool has many acquisitions and so no single one to
inherit from, and declares its own instead.

Note the word collision: "cost basis" here and in 0045 is a money quantity and is
unrelated to share count basis.

## The method parameterises the derivation

Lots are the journal and a Section 104 pool is a view over them, so per-lot and
pooled are not alternatives. The apparent fork dissolves by separating the two
jobs a lot does:

- **quantity** is per `(user, broker, account, instrument)`, because holdings
  are;
- **cost** is drawn from a scope the method chooses -- the individual lot for
  specific identification, FIFO and LIFO; a `(user, instrument)` pool for
  Section 104, because the UK pool belongs to the person rather than the account.

These never collide. A Section 104 disposal removes quantity from its own account
and draws cost from the user-level pool. So one table shape serves both methods,
and the method is an input to the derivation rather than a column on a lot:
changing it rebuilds the lot set, in the same way a new split rebuilds the
`split_adjusted_*` cache.

Section 104's first two stages are ordinary lot matching and produce ordinary
match rows; only the third consumes the pool. The 30-day stage looks **forward**,
at acquisitions in the 30 days following the disposal, which is why a disposal's
matching is not final when it is first recorded.

## Data model

```sql
-- A parcel of an instrument with a cost. One row per acquiring posting under
-- specific identification, FIFO and LIFO; one row per (user, instrument) pool
-- under Section 104, which has no acquiring posting and no single account.
lots
  id                 UUID PK
  user_id            UUID NOT NULL
  instrument_id      UUID NOT NULL
  broker, account    TEXT            -- NULL for a pool, which is person-scoped
  acquired_tx_id     UUID            -- NULL for a pool
  acquired_at        TIMESTAMPTZ     -- ordering only where it is a pad or transfer default
  qty                NUMERIC NOT NULL
  share_count_basis  DATE NOT NULL   -- the acquisition's; a pool declares its own
  cost               NUMERIC         -- NULL means the basis is not known
  cost_currency      TEXT
  unknown_qty        NUMERIC         -- pool only: the quantity whose cost is not known

-- Which lots a disposal drew from, and under which rule.
lot_disposals
  id              UUID PK
  disposal_tx_id  UUID NOT NULL      -- the reducing posting
  lot_id          UUID NOT NULL
  qty             NUMERIC NOT NULL   -- in the LOT's share_count_basis
  cost_out        NUMERIC            -- NULL where the lot's basis is not known
  matched_by      TEXT NOT NULL
  matched_at      TIMESTAMPTZ NOT NULL
```

The schema goes into `server/migrations/001_initial.sql` rather than a new
migration file.

Two properties are load-bearing, and are why this is two tables rather than one
link between two `txs` rows:

- **The link points at a lot, not at an acquiring transaction.** A Section 104
  disposal draws from the pool, and a pool is not a transaction. Were `lot_id` a
  `txs.id`, a pool disposal could only be expressed by fanning it pro rata across
  every remaining acquisition -- many rows per disposal, carrying a split that is
  arbitrary and means nothing.
- **Under Section 104 the lot machinery tracks cost only.** Quantity per account
  is already `SUM(quantity)` grouped by `(broker, account, instrument_id)` and
  needs no lots. Under specific identification and FIFO the lots track both,
  because cost attaches to an identified parcel. That is the whole of the
  difference between the methods at the storage layer.

Remaining quantity and remaining cost are derived -- `lots.qty` and `lots.cost`
less the matched sums -- rather than being mutable running columns, so there is no
running balance to drift and the re-derivation below has something exact to
assert against.

### The matching rule is recorded per match

`matched_by` is one of `NOMINATED`, `FIFO`, `LIFO`, `HIFO`, `SAME_DAY`,
`THIRTY_DAY` or `POOL`, on the row rather than read back off the user's setting.
This follows the shape 0068 uses for its transfer link -- store the link, how it
was made, and when -- for three reasons:

- **One Section 104 disposal splits across three rules**: some quantity matched
  same-day, some against acquisitions in the following 30 days, the remainder
  from the pool. A single per-user method cannot describe what happened. This is
  also the column that makes 0045's requirement to show the matching decisions
  renderable.
- **Nominated matches are input, not output.** Under specific identification the
  user or the broker chooses the lots, and that choice cannot be re-derived. A
  rebuild must keep `NOMINATED` rows and regenerate only the rest; without the
  discriminator there is nothing to tell one from the other, and a rebuild
  destroys the user's choices silently.
- **The setting moves.** It is one mutable per-user row following the user's
  jurisdiction, so a historical match that does not say how it was made becomes
  unexplainable the moment it changes.

The rule for a given disposal comes from an explicit nomination where there is
one -- from the broker's data, or from the user in the UI -- and otherwise from
the user's configured cost basis method. Nothing stores a jurisdiction today.

### Re-derivation

Beancount derives inventory by replaying the journal rather than storing it.
Deriving on every query is expensive here and storing risks drift, so lots are
stored and periodically re-derived, with the re-derivation asserting equality --
the same reconciliation pattern as 0043. Every non-pool `lots` row is
recomputable from its acquiring posting and that posting's group, which is what
lets the whole set be rebuilt when the method changes.

Derived match rows are disposable and cascade on group delete, as 0068's link
does; `NOMINATED` rows survive. A method change deletes the derived rows and
re-runs.

One recompute trigger is easy to miss. Because the 30-day rule looks forward, a
newly ingested **acquisition** can change the matching of a disposal already
recorded. Re-derivation must therefore trigger on acquisitions landing within 30
days after an existing disposal, not only on new disposals. It belongs alongside
the split recompute and the declaration re-verify in
docs/spec/bitemporality.md#retroactive-restatement.

## Gains are derived, not posted

The gain is not stored and is not a posting. Proceeds is the disposal posting's
`weight`, already stored and exact, so a realised gain is
`proceeds - SUM(cost_out)`: a subtraction over stored values, inside the
adr/0026-exact-decimals-bounded-by-closure.md boundary. Storing it would be a
second place for the same fact.

Making it a leg of the group is not available. It would require the disposal to
weigh at cost rather than at price, which would make the exact
`check_tx_group_balance()` trigger depend on lot matching, which depends on a
user-configurable method -- a database constraint that moves when a setting
changes. Gains are a derived layer over postings.

## Sequencing

This change is additive. It creates two tables and changes no column on `txs`, so
holdings, valuation and the balance constraint are untouched, and no existing row
is migrated or rewritten.

It also needs no re-ingest. Every input the derivation reads is already stored:
the acquiring posting, its cost (`weight` since 0041, the `EXPENSE` legs since
0040), its group membership, and its basis provenance in the counterparty's
`account_type`. Nothing the converters currently discard is required. The lot set
is rebuildable from whatever is in `txs` at the time, so it can be built, dropped
and rebuilt without touching ingested data.

## Scope

This issue covers the data model. The choice of matching method and the gains
computation are 0045; declaring a cost basis alongside a pad is 0075.

The reasoning behind the decisions above is in
adr/0031-lots-are-derived-and-unknown-basis-is-a-value.md.
