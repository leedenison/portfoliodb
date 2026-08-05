# Bitemporality

PortfolioDB is a record of history, and that history is not stable. A stock split
changes what a holding meant five years ago. A broker re-dates a pending trade
once it settles. A statistical agency revises an inflation index. An identifier
plugin decides today that an instrument we resolved last year was something else.

Recording only *when a thing happened* cannot express any of that. This document
defines the clocks PortfolioDB keeps, which column belongs to which, and the
rules that follow. The reasoning behind the model is in
adr/0016-bitemporal-time-model.md.

## The three clocks

| Clock | Answers | Column naming |
| --- | --- | --- |
| **Valid time** | When was this true in the world? | `*_date`, `timestamp`, `valid_from` / `valid_before` |
| **Knowledge time** | When did PortfolioDB learn it? | `first_known_at`, `last_fetched_at`, `created_at` |
| **Share count basis** | Which share count is this quantity or per-share price denominated in? | `share_count_basis` |

Valid time and knowledge time are the conventional bitemporal pair. Share count
basis is a third axis specific to this domain: it is a knowledge time belonging
to the **source** rather than to PortfolioDB, and it cannot be derived from the
other two.

The name is deliberately long. "Basis" alone means **cost basis** -- the money
paid for a lot -- everywhere else in this system, and the two are unrelated. This
document and the code never use the bare word for share denomination.

## Valid time

Valid time is when a fact was true, as asserted by whoever supplied it. It is the
clock every read API means by "as of".

| Table | Column | Meaning |
| --- | --- | --- |
| `txs` | `timestamp` | When the transaction occurred, as reported by the broker. Often date-only (see adr/0002-transaction-ingestion-model.md). |
| `eod_prices` | `price_date` | The trading date the bar describes. |
| `stock_splits` | `ex_date` | The effective / execution date. |
| `cash_dividends` | `ex_date`, `pay_date`, `record_date`, `declaration_date` | Four distinct points in the dividend's life. `declaration_date` is when the issuer announced it -- the world's knowledge time, but PortfolioDB's valid time, because what we know is that the announcement happened on that date. |
| `holding_declarations` | `as_of_date` | The date the user's declaration refers to. |
| `instruments` | `valid_from`, `valid_before`, `expiry` | When the instrument was tradeable. Descriptive only for `valid_from` / `valid_before` -- see [Instrument identity](#instrument-identity) below. |
| `instruments` | `identity_as_of` | The point in market time the stored identity reflects -- see [Instrument identity](#instrument-identity) below. |
| `price_coverage` | `covered_from`, `covered_before` | The valid-time interval a plugin was asked about for prices. |
| `corporate_event_coverage` | `covered_from`, `covered_before` | The valid-time interval a plugin was asked about. |
| `inflation_indices` | `month` | The month the index value describes. |
| `ingestion_jobs` | `period_from`, `period_before` | The valid-time window a bulk upload replaces. |
| `unhandled_corporate_events` | `ex_date` | The effective date of the event we could not handle. |

Every date interval -- wire API, database column pair, or in-memory range -- is
half-open `[from, before)` with midnight-UTC values, matching PostgreSQL's
`daterange` default. The exclusive bound is always named `before`
(`date_before`, `period_before`, `covered_before`, `Before`) so no caller has to
consult a comment to know which end is included; the closed form survives only
inside adapters for external providers that demand it
(see adr/0018-half-open-date-intervals.md).

### Instrument identity

Instrument identity is the deliberate exception: it is current state, not a
time-varying fact. `instrument_identifiers` carries no validity interval, so an
identifier resolves to whichever instrument holds it now rather than to the one
that held it on the transaction's date, and ticker reuse is not representable.
`instruments.valid_from` and `valid_before` describe when the instrument was
tradeable and no query filters on them. A merge deletes the loser outright,
leaving no record of what was believed before
(see adr/0004-instrument-resolution-and-merge.md).

`identity_as_of` is the one exception, and it is valid time rather than knowledge
time: it records the point in **market** time the stored identity reflects, which
is what an option's OCC symbol and strike are a function of. It is stamped at
derivation and moves only on genuine re-derivation -- a plugin identification, or
a retroactive option split adjustment. An incidental `EnsureInstrument` match
must leave it alone. NULL means the identity predates every split. It is compared
against `stock_splits.ex_date`, never against a knowledge time
(see adr/0017-option-identity-reflects-ex-date.md).

Derivation does not imply the present. A plugin answers about the contract it was
named, so an identity resolved from an OCC hint is only as current as that hint:
`now()` when the hint was rebased onto today for every known split, and the
hint's own vintage when a split we had not yet learned of left it alone. Stamping
`now()` regardless would mark such an identity as already reflecting a split its
symbol was derived before.

## Knowledge time

Knowledge time is when PortfolioDB learned a fact. It is recorded wherever the
source can revise what it told us.

**A knowledge-time column is named for what it means.** The generic name
`fetched_at` is not used, because it does not distinguish the two things a
knowledge timestamp can be:

- **`first_known_at`** -- when we first learned this fact. Set on insert and
  **never moved forward**, including when the fact itself is revised. This is the
  column that answers "did we know about the split when we resolved that option?"
- **`last_fetched_at`** -- when we last asked the source. Fetch bookkeeping;
  overwritten on every refresh by design. It answers "how stale is this?", never
  "when did we learn it?"

Inflation index values are the deliberate exception: they are not versioned. A
revised `index_value` replaces its predecessor in place and leaves no record that
a revision occurred, which follows from rule 8 below
(see adr/0016-bitemporal-time-model.md).

| Table | Column | Kind |
| --- | --- | --- |
| `stock_splits` | `first_known_at` | First known. Preserved across corporate-event export and import; not read by option adjustment, which keys off `ex_date` (see adr/0017-option-identity-reflects-ex-date.md). |
| `cash_dividends` | `first_known_at` | First known. |
| `eod_prices` | `last_fetched_at` | Staleness only. It carries no semantics about the price itself -- that is what `share_count_basis` is for. |
| `price_coverage` | `last_fetched_at` | When the span was last confirmed. Merged the same way as `corporate_event_coverage` below. |
| `corporate_event_coverage` | `last_fetched_at` | When the span was last confirmed. Merging spans keeps the oldest constituent's, since a union is only as freshly confirmed as its stalest part. |
| `inflation_indices` | `last_fetched_at` | Staleness only. |
| `price_fetch_blocks` | `first_blocked_at` | First known. |
| `corporate_event_fetch_blocks` | `first_blocked_at` | First known. |
| `txs`, `users`, `portfolios`, `instruments`, `ingestion_jobs`, `unhandled_corporate_events`, `holding_declarations`, `service_accounts` | `created_at` | Row audit. Not queried. |
| `holding_declarations` | `updated_at` | Row audit. |

On the wire, `ImportPricesRequest.exported_at` and
`ImportCorporateEventsRequest.exported_at` are client-declared knowledge times:
they state when the supplied data was current, and drive OCC split adjustment
during instrument resolution. See [prices.md](prices.md) and
[corporate-events.md](corporate-events.md).

Corporate events also carry knowledge time per row: `SplitRow.first_known_at`
and `CashDividendRow.first_known_at` make an export/import round trip lossless.
An importing row resolves its knowledge time from the row, else the request's
`exported_at`, else the time it is stored.

## Share count basis

A quantity of shares, and a price per share, are meaningless without knowing
which share count they are expressed in. A 2:1 split makes "100 shares at $50"
and "200 shares at $25" the same holding. The **share count basis** is the date
at which a row's share count was current.

Every source declares its own. The storage layer assumes nothing.

| Source | Declares | Because |
| --- | --- | --- |
| Broker transaction rows (CSV, extension) | the row's own transaction date | A broker log line accounts only for events prior to the trade, whatever has happened since. |
| Price plugin returning as-traded bars | each bar's `price_date` | The bar is the market as it printed that day. |
| Price plugin returning back-adjusted bars | the fetch date | The provider restated the whole series to the share count current when it answered. |
| Price import file | `price_date`, unless the row declares otherwise | Matches PortfolioDB's own export, which emits raw close. |
| Holding declaration | `as_of_date`, unless the user says otherwise | A quantity read off a record of some date is in the share count current then. A quantity read off today's holdings screen is not, and the form asks which. |

Both defaults are inferrable, and both are wrong for a source that restates. Two
sources here can: the browser extension scrapes the broker's live web UI, which
may show historical rows in post-split terms; and a price plugin switched to
adjusted output changes denomination with no schema change to signal it. So the
declaration is explicit -- `share_count_basis` on the row, and a declaration on
the plugin interface and the ingestion request that sets it.

`split_factor_at(instrument_id, share_count_basis)` converts a row from its own
basis to today's. It returns the factor as an exact rational -- a numerator and
denominator, the products of `split_to` and `split_from` over the applicable
splits -- so a caller multiplies before dividing and the division happens once.
See [corporate-events.md](corporate-events.md#adjustment) and
adr/0028-cumulative-split-factor-is-an-exact-rational.md.

The `split_adjusted_quantity` and `split_adjusted_unit_price` columns are a
derived cache of that conversion, not stored facts. A reverse split in an awkward
ratio has no exact decimal form, so they carry a declared rounding scale while
the values they derive from -- `quantity`, `unit_price`, `share_count_basis` and
the split chain -- stay exact and can be recomputed from at any time.

## Rules

1. **Every stored fact records its valid time**, except instrument identity,
   which is current state (see [Instrument identity](#instrument-identity)).
   Knowledge time is recorded wherever the source can revise the fact, except
   for inflation index values (see [Knowledge time](#knowledge-time)).
2. **A knowledge-time column is named for what it means** -- `first_known_at` or
   `last_fetched_at`, never the ambiguous `fetched_at`. A `first_known_at` never
   moves forward: revising the fact leaves it alone, and on conflict it takes
   the earlier of the stored and supplied values. A stamp can only ever be at or
   after the moment the world knew, so the earlier of two is the better one.
3. **Share-denominated values record their share count basis, declared by the
   source.** Basis is never inferred from a knowledge timestamp and never
   assumed by the storage layer.
4. **Arithmetic never mixes share counts.** Raw quantity multiplies raw price;
   split-adjusted quantity multiplies split-adjusted price. Mixing the two across
   a split silently scales the result by the split factor.
5. **Provider `adjusted_close` is a third series on a third basis** -- the
   provider's, as of `last_fetched_at`, and typically including dividend
   adjustment as well as splits. It is never an input to valuation or
   performance; it exists to cross-check the `split_adjusted_close` PortfolioDB
   derives itself.
6. **Lots inherit the share count basis of the acquisition that created them.**
   A lot-aware equivalent of `split_factor_at` reads `share_count_basis` from the
   acquiring transaction, not from the disposal or the query date.
7. **`as_of` on read APIs is valid time.** `GetHoldings(as_of)` rewinds the
   world, not our knowledge of it: it returns the transactions that had occurred
   by that date, with quantities expressed in **today's** share count.
8. **Derived values are as-of now.** `split_factor_at` bounds the product with
   `ex_date <= CURRENT_DATE`, so `split_adjusted_*` columns and every valuation
   are a function of the wall clock as well as of the stored data. Two identical
   valuation requests may legitimately return different numbers on different
   days. This is correct, and it is why there is no knowledge-time as-of query
   (see adr/0016-bitemporal-time-model.md).

## Retroactive restatement

Restatement is a change to what the system says about a date that has already
passed. It is normal, not exceptional.

| Trigger | Restates | Recompute |
| --- | --- | --- |
| A new split arrives, or a stored split's `ex_date` crosses today | `split_adjusted_*` on every price and tx for the instrument | `RecomputeSplitAdjustments` per instrument, plus a daily blanket pass for crossings -- see [corporate-events.md](corporate-events.md#daily-scheduler-planned) |
| A split arrives for an option's underlying, or a stored one's `ex_date` crosses today | The option's OCC symbol, strike and contract terms | `ProcessPendingOptionSplits`, driven by `identity_as_of` vs `ex_date` |
| A bulk upload replaces a period | Every transaction in that broker and period | Holdings and valuation follow from the transaction set; nothing is materialised |
| A transaction earlier than the current earliest arrives, or history between the start date and a declaration changes | The derived INITIALIZE transaction | See [fixed-point.md](fixed-point.md) |
| Instrument identity changes or two instruments merge | Which transactions roll up to which instrument | Holdings and valuation follow; the prior identity is not retained -- see [identifiers.md](identifiers.md) |

Restatement of a user-visible quantity should be surfaced rather than applied
silently -- see [fixed-point.md](fixed-point.md), which sets the same requirement
for recalculated INITIALIZE transactions.

## Known divergences

The model above is normative. These parts of the system do not yet comply:

| Divergence | Issue |
| --- | --- |
| No daily scheduler fires the blanket recompute, so a stored future-dated split never activates when its `ex_date` crosses | 0050 |
| Corporate actions adjust totals rather than lots | 0044 |
