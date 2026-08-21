# The cash-flow boundary is typed and classified per account

Supersedes [0020](0020-double-entry-postings.md), carrying forward its posting and
group model: a `txs` row is a **posting** whose amount is a signed `quantity` with
no type-based sign flip, the postings of one economic event belong to a **tx group**
required to sum to zero, and the invariant is enforced by
a deferred constraint trigger rather than at load time as beancount does, because
a database constraint has no equivalent of an unchecked writer. What changes is
how the non-asset side of an event is identified and how the cash-flow boundary
is derived from it.

The boundary is what makes this worth structuring at all. Money-weighted return
needs to know which flows crossed it and which were internal, and time-weighted
return needs the same boundary to sub-period correctly. With single-legged
transactions that is a classification problem over the OFX transaction types, per
broker, with nothing that fails loudly when a mapping is wrong.

## The non-asset side is a type, not an account name

Postings carry an `account_type` -- `USER`, `EQUITY`, `INCOME`, `EXPENSE`,
`IMBALANCE`, `TRANSFER_CLEARING` -- while keeping the `broker` and `account` of
the event they belong to. 0020 called instead for reserved name prefixes on
`txs.account`, after beancount's account roots.

`account` is user-supplied free text, so a name convention is unenforceable: a
broker account named `Imbalance.USD` collides with the machinery, and every read
path has to exclude reserved names with a `LIKE` that no index helps. A name also
has nowhere to record which broker account a residual came from, which is exactly
the attribution the imbalance report exists to give. And the currency is already
the posting's commodity, so encoding it in a name duplicates a typed,
foreign-keyed column in a string the two can disagree with.

## Leaving the asset accounts does not make a flow external

0020 said a flow is external iff it crosses out of the asset accounts. That is
wrong. A dividend crosses from `INCOME` into cash and a commission crosses from
cash into `EXPENSE`; both leave the asset accounts and neither is external.
Classing them so would strip dividends out of the return and report it gross of
fees.

Only `EQUITY` and a crossing to another account are external. `INCOME`, `EXPENSE`
and `IMBALANCE` are internal, being return and cost rather than contribution.
`IMBALANCE` is internal because a residual is usually a missing fee or a missing
cash leg, both internal, and because classing it external would launder bad data
out of the return instead of leaving it visible.

## Classification is per account and fixed at ingest

A posting in account A is an external flow of A iff its group has a leg outside A.
Netting between accounts is resolved per portfolio at query time and is not
stored.

Classifying flows relative to the portfolio being measured was rejected as both
unimplementable and unstable. Unimplementable because when a transfer's first side
is posted we may not know whether the other side is an account we hold, or whether
it exists in our data at all -- that is the whole reason `TRANSFER_CLEARING`
exists. Unstable because portfolios are views over editable `portfolio_filters`
([0010](0010-portfolios-as-views.md)), so adding an account to a portfolio would
silently reclassify historical flows and move every past return figure.

For the same reason there is no per-portfolio user override of internal versus
external. Membership already expresses the intent, and a toggle would be a second
place to say the same thing that can disagree with the first.

## Consequences

Matching the two sides of a transfer becomes a correctness requirement rather than
housekeeping: until a pair is matched, a transfer within one portfolio reads as a
withdrawal and an unrelated deposit. A match is recorded as a link between the two
tx groups, which supplies the account identity the membership test asks for; see
[0037](0037-transfer-matches-are-links-not-postings.md).

Converters, not the server, emit income and expense legs
([0021](0021-converters-own-transaction-grouping.md)). The server cannot
distinguish an uncategorised dividend from a genuinely incomplete trade, so every
unbalanced residual routes to `IMBALANCE` and early imbalance figures are
dominated by uncategorised income rather than by the missing fees the mechanism is
aimed at.

Typing the accounts changes the read path, where grouping alone did not. Holdings
and portfolio views filter to `account_type = 'USER'` so no residual or in-flight
balance appears as a position. Valuation is a separate question: excluding
`TRANSFER_CLEARING` makes a holding vanish for the days between the two sides of a
matched transfer, so in-flight value is included for matched pairs whose accounts
are both portfolio members, and excluded otherwise. Valuation and the flow query
read one view, `portfolio_in_flight_txs`, so they cannot disagree about what nets
-- a disagreement would be a value series and a return series computed over
different perimeters.

**A historical value series moves when membership changes or when the matcher
runs.** This ADR rejects portfolio-relative *classification* partly because adding
an account would move every past return figure, then accepts exactly that for
netting and valuation, which are read at query time. It is the price of not
storing a decision that depends on data which has not arrived.

0020's remaining consequences stand. An INITIALIZE row
([0011](0011-synthetic-initialize-transactions.md)) is a pad with no counterparty
and needs an `EQUITY` posting to satisfy the invariant, and the invariant is
awkward to state while `quantity` and `unit_price` are `DOUBLE PRECISION`
([0026](0026-exact-decimals-bounded-by-closure.md)).
