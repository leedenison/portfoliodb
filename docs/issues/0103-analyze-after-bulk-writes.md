---
status: open
title: Keep planner statistics current across bulk writes
milestone: P01
---

Decide and implement when PortfolioDB refreshes planner statistics, so a query
run straight after a bulk write is not planned against the table as it was
before it.

## Motivation

An import writes a large fraction of `txs` in one transaction, and the thing a
user does next is look at a valuation. Autovacuum decides when to analyse on its
own schedule, so in that window the planner costs the valuation from statistics
describing the table before the import. Nothing is wrong with the answer; what
changes is the plan chosen to reach it.

That window is measurable, because the valuation benchmark used to sit in it by
accident. It seeds inside a transaction that rolls back, so autovacuum never saw
the data and the planner had no statistics at all. The join above the day grid
was estimated at 111 rows against an actual 91,350; adding an `ANALYZE` moved it
to 11,208. Three orders of magnitude of error became one. An earlier attempt at
the same query, correct but differently shaped, was three times slower for
exactly this reason: at 111 rows a nested loop looked cheap, and it discarded 334
million rows. The benchmark now analyses after seeding, which is why it measures
the server rather than the harness -- but that fix does not reach production,
where the statistics exist and are merely stale.

Also worth settling here: the same argument applies to price and corporate event
fetches, which write in bulk on their own cadence, and `eod_prices` is a
hypertable whose chunks are analysed individually.

Not a query change. The valuation query has been through this and the remaining
estimate error is in a range join postgres has no selectivity model for, which no
statistics strategy improves. Two things were tried and rejected on measurement:
a calendar table for the day grid, which fixed the leaf estimate and left the
root where it was, and extended statistics on the transaction grouping columns,
which moved one estimate closer and another further away. Both are recorded in
comments where someone standing in the right place would think of them.

## Scope

- Where an explicit `ANALYZE` belongs: the ingestion worker after a tx import,
  the price and corporate event workers after a fetch cycle, or neither if
  autovacuum can be tuned to cover it instead.
- Whether per-table autovacuum settings on `txs` and `eod_prices` are the better
  answer than an explicit statement, given a bulk write is exactly the case the
  default scale factor is slowest to react to.
- What it costs on a large table, and whether it belongs inside the import's
  transaction or after it commits.
