# Bitemporal time model: three clocks, basis declared by the source

PortfolioDB records history that later changes -- splits restate quantities,
brokers re-date pending trades, agencies revise indices, identification improves.
The system keeps **three** clocks rather than the conventional bitemporal two:
valid time (when a fact was true), knowledge time (when we learned it), and
**share count basis** (which share count a quantity or per-share price is
denominated in). See [bitemporality.md](../spec/bitemporality.md).

## Share count basis is declared, not inferred

Basis belongs to the **source**, so the source declares it: a price plugin states
whether its bars are as-traded or back-adjusted, and an ingestion request may
state the vintage of the statement it carries. Storage assumes nothing.

Inferring it from a knowledge timestamp -- that a provider back-adjusts its
series to the moment it answers -- is the error this replaces, and it fails
silently: both price plugins in fact return as-traded bars, so every backfilled
bar was adjusted by a factor of 1 and `split_adjusted_close` equalled the raw
pre-split price. A declaration keeps a plugin switched to adjusted output, or a
broker that restates historical rows, from corrupting the adjustment with nothing
in the schema to signal it. Which sources declare what is in
[0056](0056-a-relaying-source-cannot-convert-back.md).

The name is long on purpose. "Basis" already means **cost basis** here -- the
money paid for a lot -- so neither the spec nor the code uses the bare word for
share denomination.

## Knowledge-time columns are named for their semantics

A knowledge timestamp is either "when we first learned this" or "when we last
asked" -- never both, and the two behave oppositely on revision. Columns are
therefore `first_known_at` or `last_fetched_at`, and the name is a claim the write
path must honour. Calling both `fetched_at` leaves the distinction to whether a
given `ON CONFLICT DO UPDATE` happens to include the column, which is invisible at
the point of use.

## No knowledge-time as-of queries

There is no way to ask "what did we believe on 2024-01-01?", and there will not
be. Answering it needs versioned history on prices, splits and transactions -- a
large, invasive cost for a benefit only audit and reproducibility need, and
ingestion is already knowledge-lossy by replacement
([0002](0002-transaction-ingestion-model.md)). Recording the three clocks
correctly is worth doing on its own; the accepted consequence is that every
derived value is as-of now and two identical valuation requests may legitimately
differ across days.

Should a figure ever need defending, the cheaper starting point is to stamp each
valuation response with its compute time and the split state it reflects, which
makes a difference between two runs explainable without making it reproducible.

Inflation indices looked like the cheapest place to pilot versioned history -- the
valid time is a fixed month while the value can legitimately change -- and were
rejected too. Nothing consumes them beyond gap detection and the admin listing,
so the real-return figure whose reproducibility would justify the history does not
yet exist. ONS, the only implemented provider, does not revise CPI or CPIH once
published; it rebases, and a real return is a ratio of two index values, which a
rebasing leaves unchanged.

## Consequences

- Constrains [0002](0002-transaction-ingestion-model.md) (ingestion by
  replacement), [0005](0005-corporate-events-design.md) (corporate events) and
  [0014](0014-extension-transaction-import.md) (extension import): each is a place
  where a source can restate, and each must declare rather than assume.
- A revised inflation index silently replaces its predecessor, with no record that
  a revision happened.
- A figure PortfolioDB reported in the past cannot be reproduced, and no tracked
  work will make it possible. A caller comparing two runs of the same request has
  nothing in the response explaining a difference.
- Valuation must pair split-adjusted quantity with split-adjusted close. Pairing
  raw quantity with raw close is only correct if the split transaction itself is
  stored, and 0005 deliberately drops `TX_TYPE=SPLIT` rows.
