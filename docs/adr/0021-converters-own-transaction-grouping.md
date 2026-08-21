---
status: partly superseded by ADR-0041
---

# Converters own transaction grouping; the server never derives a leg

Partly superseded by [0041](0041-server-owns-transaction-grouping.md), which moves the
grouping decision to the server. What survives is everything below about ledger
content: the server still does not invent a leg, does not derive a cash side from
`quantity * unit_price`, and does not fold a fee into a cash amount. Grouping decides
which postings belong together, not what postings exist.

The server persists the postings it is given: it does not infer a missing leg, does not
pair rows, and does not fold a fee into a cash amount.

The alternative was to derive the cash leg server-side as `-(quantity * unit_price)`
from the security row. That double-counts for any broker that already reports its own
cash leg, and Fidelity does: a `Sell` is accompanied by a `Cash In From Sell` row for
the same money, and the two match exactly on `|Amount|` within an account and
completion date across all 55 sells in the sample export. Deriving a second cash leg on
top would post the proceeds twice.

Fees are therefore postings, not columns. A dealing fee, PTM levy or stamp duty is a
cash posting with `type=INVEXPENSE`, which is the only representation that works when a
broker charges them separately from the trade and on a different date. Adding `fees` and
`net_amount` columns to the standard format was rejected: it would express a
Fidelity-shaped fee twice, and a broker that nets commission into a single total (IBKR
reports `Amount` and no commission column) can be split by its own converter, which is
the one place that knows the broker's conventions.

## Consequences

Every broker oddity lands in one converter rather than being spread between a
converter, a format column and a server-side rule. The cost is that a converter which
supplies neither a cash row nor a unit price produces an unbalanced group; that residual
is routed to an explicit imbalance account rather than rejected, so coverage tightens
per broker over time instead of in a single cut-over.

The rule against pairing is about ledger content, not about pairing as such. The server
pairs the two sides of a transfer after the fact, which creates no posting and no group
and spans uploads no converter can see; see
[0037](0037-transfer-matches-are-links-not-postings.md).

The original argument for converter-owned pairing rested on a premise that no longer
holds -- that the broker's reference numbers were discarded before the data reached the
standard format. They are stored on the posting, which is what let 0041 move the
decision.
