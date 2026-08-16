---
status: closed
title: A posting carries an order date and a trade date
milestone: M15
---

Replace `Posting.timestamp` with `order_date` and add `trade_date`, both required,
so that a charge and the trade it was levied on can be bucketed together.

## Motivation

Every converter chose which of its source's dates to put in the single `timestamp`,
and they chose differently: Fidelity's took the Completion date, the OFX parser took
`DTTRADE`. So the field already meant different things per broker.

Neither reading buckets a per-trade charge with its trade. Over the 689-row Fidelity
master, a trade completes T+2 to T+4 while its dealing fee completes T+0 or T+1, so
71 of 91 trades have Order != Completion while 72 of 94 charges have them equal. The
order date is the one they agree about, and the converters read it and threw it away.

## Design

`order_date` is the date of record -- windows, listings and their page cursor,
`tx_groups.timestamp`, valuation, holdings, the residual report, and the grouping
rules that bucket on a day. `trade_date` is when the transaction took effect, and is
what `share_count_basis` defaults from.

A source reporting one date writes it to both, which says the two coincide for it
rather than that one is unknown. Ingest validation rejects a posting missing either.

See adr/0051-a-posting-carries-an-order-date-and-a-trade-date.md.

## Consequences

The trade rules keep claiming exactly what they claimed before: all 91 trade/cash
pairs in the master share an order date, and `Deposit` has no date bound.

Because `timestamp` was replaced rather than supplemented, `DateQuery`,
`PostingsByDates` and `expandByDay` bucket on the order date with no change to any of
them -- no new `GroupingReader` method and no new index. That is what the charge rules
in 0110 and 0111 build on.

The grouping fixtures in `server/grouping/testdata/` carry the same instant in both
dates, since they were extracted from sources that stated one. They therefore do not
yet exercise the split; the fixtures that do arrive with 0111.
