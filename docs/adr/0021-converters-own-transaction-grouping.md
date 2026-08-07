# Converters own transaction grouping; the server never derives a leg

An upload is a list of postings, and the broker-specific converter decides which of
them are legs of one economic event. The server persists what it is given: it does
not infer a missing leg, does not pair rows, and does not fold a fee into a cash
amount.

The alternative was to derive the cash leg server-side as
`-(quantity * unit_price)` from the security row. That double-counts for any broker
that already reports its own cash leg, and Fidelity does: a `Sell` is accompanied by
a `Cash In From Sell` row for the same money, and the two match exactly on
`|Amount|` within an account and completion date across all 55 sells in the sample
export. Deriving a second cash leg on top would post the proceeds twice. Pairing them
after the fact is not possible either, because by the time the data reaches the
standard format the broker's own reference numbers have been discarded -- only the
converter still has them.

Fees are therefore postings, not columns. A dealing fee, PTM levy or stamp duty is a
cash posting with `type=INVEXPENSE`, which is already how the Fidelity converter
emits them and is the only representation that works when a broker charges them
separately from the trade and on a different date. Adding `fees` and `net_amount`
columns to the standard CSV was considered and rejected: it would express a
Fidelity-shaped fee twice, and a broker that nets commission into a single total
(IBKR reports `Amount` and no commission column) can be split by its own converter,
which is the one place that knows the broker's conventions.

## Consequences

Every broker oddity lands in one converter rather than being spread between a
converter, a CSV column and a server-side rule. The cost is that a converter which
supplies neither a cash row nor a unit price produces an unbalanced group; that
residual is routed to an explicit imbalance account rather than rejected, so
coverage tightens per broker over time instead of in a single cut-over.

The rule against pairing is about ledger content, not about pairing as such. The
server pairs the two sides of a transfer after the fact, which creates no posting and
no group and spans uploads no converter can see. The premise above -- that the
broker's reference numbers are gone by the time data reaches the standard format --
no longer holds either: they are stored on the posting. See
[0037](0037-transfer-matches-are-links-not-postings.md).
