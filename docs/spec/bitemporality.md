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
| **Share count basis** | Which share count is this quantity or per-share price denominated in? | fixed by convention; see [Share count basis](#share-count-basis) |

Valid time and knowledge time are the conventional bitemporal pair. Share count
basis is a third question this domain has to answer, but it is not a third stored
axis: the API fixes the answer by convention and every source converts to it, so
nothing has to be recorded or read back.

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
| `instrument_identifiers` | `valid_from`, `valid_before` | The interval a name was correct for the instrument -- see [Instrument identity](#instrument-identity) below. |
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

A name is valid over an interval, not for all time. `instrument_identifiers`
carries a half-open `[valid_from, valid_before)` in **market** time: `valid_from`
is when the name became correct for the instrument -- the vintage of the source
that supplied it, or the `ex_date` of the split that minted it -- and a NULL
`valid_before` means it is the name the instrument wears now. It is valid time
rather than knowledge time, and it is compared against `stock_splits.ex_date`,
never against a knowledge time
(see adr/0055-identifier-validity-is-an-interval.md).

The interval is per name because names do not move together: an OCC symbol
encodes a strike and is restated by a split, an ISIN is not, a
`BROKER_DESCRIPTION` never was. A split closes the OCC symbol at its `ex_date`
and mints the adjusted one from it, so a broker file exported either side of the
split resolves to the same contract.

A name only ever gets its bounds when it is written. Matching an existing
instrument is not evidence that any of its names became correct today, so an
incidental `EnsureInstrument` touch must leave them alone -- moving a `valid_from`
forward is what would tell the option-split pass that a symbol already reflects a
split it was derived before.

Derivation does not imply the present. A plugin answers about the contract it was
named, so a name resolved from an OCC hint is only as current as that hint: today
when the hint was rebased across every known split, and the hint's own vintage
when a split we had not yet learned of left it alone.

**Uniqueness is interval-aware.** A name denotes one instrument at a time, stated
as a GIST exclusion constraint on overlapping validity rather than as a unique
index. The case that forces it is not ticker reuse but two options on one
underlying: a 2:1 split halves every strike, so the 100-strike call's new symbol
is character-for-character the 50-strike's old one.

**What is still current state.** A lookup by value alone takes the name in force,
falling back to the most recently closed one, so a pre-split symbol still
resolves. Asking what a value denoted on a given date is
[0122](../issues/0122-resolve-identity-as-of-a-date.md), not this.
`instruments.valid_from` and `valid_before` describe when the instrument was
tradeable and no query filters on them. A merge still deletes the loser outright,
leaving no record of what was believed before, though the loser's names travel to
the survivor with their intervals intact
(see adr/0004-instrument-resolution-and-merge.md).

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
| `eod_prices` | `last_fetched_at` | Staleness only. It carries no semantics about the price itself, whose denomination is its own `price_date`. |
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

Transaction ingestion states the same thing on `UpsertTxsRequest.exported_at`,
and an archive's transaction part takes it from the envelope. It is one value per
upload or per document rather than one per posting: a file has one export, and the
symbol it names a contract by is the one current at that export. An upload that
states nothing is its own export and the server stamps its clock at receipt --
the vintage is never inferred from a posting's `trade_date`, which is the error
this rule exists to stop.

Corporate events also carry knowledge time per event: `Split.first_known_at`
and `CashDividend.first_known_at` make an export/import round trip lossless.
An importing row resolves its knowledge time from the row, else the request's
`exported_at`, else the time it is stored.

## Share count basis

A quantity of shares, and a price per share, are meaningless without knowing
which share count they are expressed in. A 2:1 split makes "100 shares at $50"
and "200 shares at $25" the same holding. The **share count basis** is the date
at which a row's share count was current.

It is fixed by convention rather than declared. Each kind of row has one basis
and only one, and a source holding its data on any other basis converts before it
uploads:

| Row | Basis | Because |
| --- | --- | --- |
| A posting | its own `trade_date` | A trade happened in the share count of the day it happened in. |
| A price bar | its own `price_date` | The bar is the market as it printed that day, so a provider is asked for unadjusted output. |
| A holding declaration | its own `as_of_date` | The assertion is about a date, so it is denominated in that date. |
| An identifier | the exporting file's `exported_at`, else the upload's own receipt | An OCC symbol encodes a strike, so it moves under a split. A file states identity as it stood when the file was written. |

The alternative was a per-row `share_count_basis` that let a source say which
basis it had used. It was carried on postings, price rows and declarations, and
in three years of real broker data nothing ever set it: brokers report as-traded,
and every price plugin asks for unadjusted bars. What it did instead was let a
source restate silently and stay conforming, because a row that restates without
declaring is indistinguishable from one that does not.

A convention moves that work to where the knowledge is. A source that restates
knows it restated and knows the ratio it used -- that is how it restated -- so it
can convert back. A source that does not restate does nothing. Neither has to
describe itself, and neither can misdescribe itself.

See adr/0054-share-count-basis-is-a-convention.md for the decision and what it
cost. For a person entering a holding declaration the convention is the whole of
the user interface: the form asks for the quantity held on the date, not the quantity
a screen shows today. A field asking which basis they had used would be answered
wrongly by anyone who needed it and redundantly by anyone who did not.

`split_factor_at(instrument_id, basis_date)` converts a row from its own basis
to today's, where `basis_date` is whichever of the dates above the row carries. It returns the factor as an exact rational -- a numerator and
denominator, the products of `split_to` and `split_from` over the applicable
splits -- so a caller multiplies before dividing and the division happens once.
See [corporate-events.md](corporate-events.md#adjustment) and
adr/0028-cumulative-split-factor-is-an-exact-rational.md.

The `split_adjusted_quantity` and `split_adjusted_unit_price` columns are a
derived cache of that conversion, not stored facts. A reverse split in an awkward
ratio has no exact decimal form, so they carry a declared rounding scale while
the values they derive from -- `quantity`, `unit_price`, the row's own date and
the split chain -- stay exact and can be recomputed from at any time.

## Rules

1. **Every stored fact records its valid time**, instrument identity included:
   a name carries the interval it was correct over
   (see [Instrument identity](#instrument-identity)). Knowledge time is recorded
   wherever the source can revise the fact, except for inflation index values
   (see [Knowledge time](#knowledge-time)).
2. **A knowledge-time column is named for what it means** -- `first_known_at` or
   `last_fetched_at`, never the ambiguous `fetched_at`. A `first_known_at` never
   moves forward: revising the fact leaves it alone, and on conflict it takes
   the earlier of the stored and supplied values. A stamp can only ever be at or
   after the moment the world knew, so the earlier of two is the better one.
3. **Share-denominated values are on the basis their kind fixes, and a source
   that holds otherwise converts before it uploads.** A posting is on its
   `trade_date`, a price bar on its `price_date`, a declaration on its
   `as_of_date`. Nothing is inferred from when a row was fetched.
4. **Arithmetic never mixes share counts.** Raw quantity multiplies raw price;
   split-adjusted quantity multiplies split-adjusted price. Mixing the two across
   a split silently scales the result by the split factor.
5. **Provider `adjusted_close` is a third series on a third basis** -- the
   provider's, as of `last_fetched_at`, and typically including dividend
   adjustment as well as splits. It is never an input to valuation or
   performance; it exists to cross-check the `split_adjusted_close` PortfolioDB
   derives itself.
6. **Lots inherit the share count basis of the acquisition that created them.**
   A lot-aware equivalent of `split_factor_at` reads the trade date from the
   acquiring transaction, not from the disposal or the query date. A Section 104
   pool has many acquisitions and so none to inherit from, and declares its own
   (see adr/0031-lots-are-derived-and-unknown-basis-is-a-value.md).
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
| A split arrives for an option's underlying, or a stored one's `ex_date` crosses today | The OCC symbol, strike and contract terms of the options listed on the ex_date | `ProcessPendingOptionSplits`, driven by the OCC symbol's `valid_from` vs `ex_date`, bounded at the option's `expiry` |
| A bulk upload replaces a period | Every transaction in that broker and period | Holdings and valuation follow from the transaction set; nothing is materialised |
| A transaction earlier than the current earliest arrives, or history between the start date and a declaration changes | The derived INITIALIZE transaction | See [fixed-point.md](fixed-point.md) |
| Instrument identity changes or two instruments merge | Which transactions roll up to which instrument | Holdings and valuation follow; the prior identity is not retained -- see [identifiers.md](identifiers.md) |
| An acquisition arrives within 30 days after a recorded disposal | Which lots that disposal matched, under Section 104's forward-looking 30-day rule | Re-derive the disposal's matches -- see 0044 |

Restatement of a user-visible quantity should be surfaced rather than applied
silently -- see [fixed-point.md](fixed-point.md), which sets the same requirement
for recalculated INITIALIZE transactions.

## Known divergences

The model above is normative. These parts of the system do not yet comply:

| Divergence | Issue |
| --- | --- |
| No daily scheduler fires the blanket recompute, so a stored future-dated split never activates when its `ex_date` crosses | 0050 |
| Corporate actions adjust totals rather than lots | 0044 |
