# Bitemporal time model: three clocks, basis declared by the source

PortfolioDB records history that later changes -- splits restate quantities,
brokers re-date pending trades, agencies revise indices, identification improves.
The system keeps **three** clocks rather than the conventional bitemporal two:
valid time (when a fact was true), knowledge time (when we learned it), and
**share count basis** (which share count a quantity or per-share price is
denominated in). See [bitemporality.md](../spec/bitemporality.md).

## Share count basis is recorded, not inferred

Basis was previously inferred from `eod_prices.fetched_at`, on the assumption
that a provider back-adjusts its series to the moment it answers. Both price
plugins in fact return as-traded bars, for which the correct basis is
`price_date`, so every backfilled bar was adjusted by a factor of 1 and
`split_adjusted_close` silently equalled the raw pre-split price.

The fix is not to swap one inferred date for another. Basis belongs to the
**source**, so the source declares it: the price plugin interface states whether
its bars are as-traded or back-adjusted, and the transaction ingestion request
may state the vintage of the statement it carries. Storage assumes nothing. This
keeps a plugin switched to adjusted output, or a broker that restates historical
rows, from silently corrupting the adjustment with no schema change to signal it.

The name is long on purpose. "Basis" already means **cost basis** in this system
-- the money paid for a lot, an entirely unrelated concept that will sit in an
adjacent spec once lot identity lands. Neither the spec nor the code uses the
bare word for share denomination.

## Knowledge-time columns are named for their semantics

A knowledge timestamp is either "when we first learned this" or "when we last
asked" -- never both, and the two behave oppositely on revision. Calling both
`fetched_at` left the distinction to whether a given `ON CONFLICT DO UPDATE`
happened to include the column, which is invisible at the point of use and was
inconsistent between `stock_splits` (preserved) and `cash_dividends`
(overwritten). Columns are therefore `first_known_at` or `last_fetched_at`, and
the name is a claim the write path must honour.

## No knowledge-time as-of queries

There is no way to ask "what did we believe on 2024-01-01?", and adding one is
deferred. Answering it needs versioned history on prices, splits and
transactions -- a large, invasive cost for a benefit only audit and
reproducibility need, and transaction ingestion is already knowledge-lossy by
replacement (see 0002-transaction-ingestion-model.md). Recording the three clocks
correctly is the prerequisite for that work and is worth doing on its own; the
consequence, accepted here, is that every derived value is as-of now and two
identical valuation requests may legitimately differ across days.

Inflation indices looked like the cheapest place to pilot versioned history --
the valid time is a fixed month while the value can legitimately change -- and
were rejected too. Nothing consumes them beyond gap detection and the admin
listing, so the real-return figure whose reproducibility would justify the
history does not yet exist. ONS, the only implemented provider, does not revise
CPI or CPIH once published; it rebases, and a real return is a ratio of two index
values, which a rebasing leaves unchanged. Storing vintages nobody reads, of
revisions that mostly do not change the answer, is not worth the schema.

## Consequences

- Constrains 0002 (ingestion by replacement), 0005 (corporate events) and 0014
  (extension import): each is a place where a source can restate, and each must
  now declare rather than assume.
- A revised inflation index silently replaces its predecessor. There is no record
  that a revision happened, and no way to reproduce a real return computed from
  the earlier value.
- Instrument identity is current state for the same reason, decided in
  0004-instrument-resolution-and-merge.md: a reused identifier rewrites how every
  transaction that resolved through it is interpreted, and a merge leaves no
  record of the loser.
- Valuation must pair split-adjusted quantity with split-adjusted close. Pairing
  raw quantity with raw close is only correct if the split transaction itself is
  stored, and 0005 deliberately drops `TX_TYPE=SPLIT` rows.
