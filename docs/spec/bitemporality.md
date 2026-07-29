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
| **Valid time** | When was this true in the world? | `*_date`, `timestamp`, `valid_from` / `valid_to` |
| **Knowledge time** | When did PortfolioDB learn it? | `first_known_at`, `last_fetched_at`, `identified_at`, `created_at` |
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
| `instruments` | `valid_from`, `valid_to`, `expiry` | When the instrument was tradeable. |
| `corporate_event_coverage` | `covered_from`, `covered_to` | The valid-time interval a plugin was asked about. |
| `inflation_indices` | `month` | The month the index value describes. |
| `ingestion_jobs` | `period_from`, `period_to` | The valid-time window a bulk upload replaces. |
| `unhandled_corporate_events` | `ex_date` | The effective date of the event we could not handle. |

Date ranges are half-open `[from, to)` with midnight-UTC values, matching
PostgreSQL's `daterange` default (see adr/0007-calendar-day-valuation.md).

## Knowledge time

Knowledge time is when PortfolioDB learned a fact. It is recorded wherever the
source can revise what it told us.

**A knowledge-time column is named for what it means.** The generic name
`fetched_at` is not used, because it does not distinguish the two things a
knowledge timestamp can be:

- **`first_known_at`** -- when we first learned this fact. Set on insert and
  **never overwritten**, including when the fact itself is revised. This is the
  column that answers "did we know about the split when we resolved that option?"
- **`last_fetched_at`** -- when we last asked the source. Fetch bookkeeping;
  overwritten on every refresh by design. It answers "how stale is this?", never
  "when did we learn it?"

| Table | Column | Kind |
| --- | --- | --- |
| `stock_splits` | `first_known_at` | First known. Compared against `instruments.identified_at` to decide whether an option's OCC symbol still needs retroactive adjustment. |
| `cash_dividends` | `first_known_at` | First known. |
| `eod_prices` | `last_fetched_at` | Staleness only. It carries no semantics about the price itself -- that is what `share_count_basis` is for. |
| `corporate_event_coverage` | `last_fetched_at` | When the span was last confirmed. Collapsed to the merge time when spans merge. |
| `inflation_indices` | `last_fetched_at` | Staleness only. |
| `price_fetch_blocks` | `first_blocked_at` | First known. |
| `corporate_event_fetch_blocks` | `first_blocked_at` | First known. |
| `instruments` | `identified_at` | When identification last ran for this instrument. |
| `txs`, `users`, `portfolios`, `instruments`, `ingestion_jobs`, `unhandled_corporate_events`, `holding_declarations`, `service_accounts` | `created_at` | Row audit. Not queried. |
| `holding_declarations` | `updated_at` | Row audit. |

On the wire, `ImportPricesRequest.exported_at` is a client-declared knowledge
time: it states when the supplied data was current, and drives OCC split
adjustment during instrument resolution. See [prices.md](prices.md).

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

Both defaults are inferrable, and both are wrong for a source that restates. Two
sources here can: the browser extension scrapes the broker's live web UI, which
may show historical rows in post-split terms; and a price plugin switched to
adjusted output changes denomination with no schema change to signal it. So the
declaration is explicit -- `share_count_basis` on the row, and a declaration on
the plugin interface and the ingestion request that sets it.

`split_factor_at(instrument_id, share_count_basis)` converts a row from its own
basis to today's. See [corporate-events.md](corporate-events.md#adjustment).

## Rules

1. **Every stored fact records its valid time.** Knowledge time is recorded
   wherever the source can revise the fact.
2. **A knowledge-time column is named for what it means** -- `first_known_at` or
   `last_fetched_at`, never the ambiguous `fetched_at`. A `first_known_at` is
   never included in an `ON CONFLICT DO UPDATE` set.
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
| A split arrives for an option's underlying | The option's OCC symbol, strike and contract terms | `ProcessOptionSplits`, gated on `first_known_at` vs `identified_at` |
| A bulk upload replaces a period | Every transaction in that broker and period | Holdings and valuation follow from the transaction set; nothing is materialised |
| A transaction earlier than the current earliest arrives, or history between the start date and a declaration changes | The derived INITIALIZE transaction | See [fixed-point.md](fixed-point.md) |
| Instrument identity changes or two instruments merge | Which transactions roll up to which instrument | Holdings and valuation follow; see [identifiers.md](identifiers.md) |

Restatement of a user-visible quantity should be surfaced rather than applied
silently -- see [fixed-point.md](fixed-point.md), which sets the same requirement
for recalculated INITIALIZE transactions.

## Known divergences

The model above is normative. These parts of the system do not yet comply:

| Divergence | Issue |
| --- | --- |
| Transactions have no `share_count_basis`: they are adjusted against `timestamp`, so a broker that restates historical rows cannot say so | in flight |
| Valuation pairs raw quantity with raw close while `TX_TYPE=SPLIT` rows are dropped at ingestion, so a forward split shows as a discontinuity | in flight |
| No daily scheduler fires the blanket recompute, so a stored future-dated split never activates when its `ex_date` crosses | 0050 |
| Corporate event export and import carry no knowledge time, so a re-import cannot reproduce the original adjustment state; `corporate_event_coverage` collapses every span's `last_fetched_at` on merge | 0051 |
| A revised inflation index overwrites the prior value, so a previously published real-return figure cannot be reproduced | 0052 |
| Instrument identity has no valid-time dimension: `instruments.valid_from` / `valid_to` are never queried, `instrument_identifiers` cannot express ticker reuse, and a merge leaves no record of what was believed before | 0053 |
| There is no knowledge-time as-of query, so a past valuation cannot be reproduced | 0054 |
| `identified_at` is bumped by every `EnsureInstrument` call, not only by split-aware re-identification, which can disarm the retroactive option-split guard | 0055 |
| Date intervals are half-open in some API messages and closed in others | 0056 |
| Holding declarations permit one assertion per holding, so a corrected declaration destroys the prior one | 0043 |
| Corporate actions adjust totals rather than lots | 0044 |
