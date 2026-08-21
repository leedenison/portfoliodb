# A posting carries an order date and a trade date

**A posting carries two dates, `order_date` and `trade_date`, both required.
`order_date` is the date the posting is filed under. A source reporting one date
writes it to both.**

A single `timestamp` left each converter choosing which of the source's dates to put
in it -- Fidelity's the Completion date, the OFX parser `DTTRADE` -- so the field
already meant different things per broker, and neither reading was the one grouping
needed.

## One date cannot bucket a charge with its trade

A broker reports a per-trade charge as a row of its own, dated by itself. Measured
over the 689-row Fidelity master in `local/masters/`:

| | order == completion | order != completion |
| --- | --- | --- |
| Buy / Sell rows | 20 | 71 |
| Dealing Fee, PTM Levy, Fx Charge, Stamp Duty | 72 | 22 |

A trade completes T+2 to T+4; its dealing fee completes T+0 or T+1. So a rule
bucketing on the completion date puts a charge two to four days away from the trade it
was levied on, and one bucketing on the settlement date of the charge does no better.
The **order date is the date the two agree about**, and it is exact: every one of the
36 buys in the master has a gap between its stated `Amount` and its cash leg equal to
a sum of charge rows sharing its account and order date.

## Why replace rather than add an optional second date

Replacing means `DateQuery`, `PostingsByDates` and `expandByDay` bucket on the order
date with no change to any of them -- the column is renamed underneath, and the
grouping rules that already bucket on a day get the right date for free, with no new
`GroupingReader` method and no new index. That matters because
[0050](0050-grouping-recomputes-a-neighbourhood.md) makes "state your reach as a
bounded indexed query" an admissibility test, and a new access path would have been a
new method and a new statement behind it.

It is safe for the rules that already exist: all 91 trade/cash pairs in the master
share an order date, so `Disposal`, `Acquisition` and `CashTrade` claim exactly what
they claimed before, and `Deposit` has no date bound.

## Why both are required, and duplicated where a source has one

An OFX statement has no `DTSETTLE` tag at all and a Schwab CSV has one date column.
Those sources could have left `trade_date` unset, with an absent value reading as the
order date, which is the convention `share_count_basis` uses.

They duplicate instead. A source that reports one date is saying **the two coincide
for it**, which is a different statement from "one is unknown", and it is the true one:
an IBKR `DTTRADE` really is both. Writing it twice says so, and lets every reader take
either field without first asking whether this particular broker distinguishes them. It
also makes an absent date a converter that forgot rather than a source that did not
distinguish, which is a fault worth reporting; ingest validation rejects it.

## Which date each thing uses

`order_date` is the date of record: the replace window, the listing order and its page
cursor, `tx_groups.timestamp` (the earliest of a group's legs), valuation, holdings and
the residual report.

`trade_date` is what `share_count_basis` defaults from. The as-traded convention that
default encodes -- a broker log line accounts only for events prior to the trade -- is
about when the trade happened, not when it was ordered.

The `INITIALIZE` pad writes its portfolio start date to both. It is a synthetic opening
balance rather than a transaction anyone ordered, so it has no lag to state.

## Consequences

`Posting.timestamp` and `Tx.timestamp` are gone rather than reserved, following the
convention that retires field numbers outright.

A Fidelity row still Pending at export time has no completion date, so its order date
stands in for its trade date. That is the source declining to say when it settles
rather than saying the two coincide, and it is the one case where the duplication above
is a stand-in rather than a statement. It also removes the duplicate-row hazard
described in [broker-import-extension.md](../spec/broker-import-extension.md): a
Pending row used to be re-dated from its order date to its completion date once it
settled, moving it across a replace window boundary, and its `order_date` is now stable
across that transition.

For Fidelity, `trade_date` carries the Completion date, which is a settlement date; for
IBKR it carries `DTTRADE`, an execution date. The field means "when it took effect" and
both readings satisfy that, but they are not the same event, and a source that reported
all three would need a third field.
