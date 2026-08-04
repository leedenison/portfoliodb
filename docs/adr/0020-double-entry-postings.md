---
status: superseded by ADR-0022
---

# Transactions are double-entry postings grouped by economic event

Superseded by [0022](0022-typed-per-account-cash-flow-boundary.md), which keeps
the posting and group model below but replaces the non-asset account vocabulary
with a typed `account_type`, and corrects the boundary rule stated here.

A `txs` row is a **posting**, and the postings of one economic event belong to a
**tx group** whose amounts are required to sum to zero, in the manner of beancount
and ledger. The table was already a posting table in all but name -- the commodity is
`instrument_id` (currencies are instruments), the amount is a signed `quantity`, the
account is `(broker, account)`, and holdings are `SUM(quantity)` with no type-based
sign flip. What was missing was the parent record and the invariant.

The reason is the portfolio cash-flow boundary. Money-weighted return needs to know
which flows crossed it (deposits, withdrawals) and which were internal (a buy
converts cash into shares inside the boundary), and time-weighted return needs the
same boundary to sub-period correctly. With single-legged transactions that is a
classification problem over the ~22 OFX transaction types, per broker, with no
structural guarantee and nothing that fails loudly when a mapping is wrong. With
grouped postings and a small non-asset account vocabulary it is structural: a flow is
external iff it crosses out of the asset accounts.

The invariant will be enforced by a deferred constraint trigger in the database
rather than at load time as beancount does, because a database constraint has no
equivalent of an unchecked writer: no code path, no bad import and no manual psql
session can leave an unbalanced group behind.

## Consequences

An INITIALIZE row (see [0011](0011-synthetic-initialize-transactions.md)) is a pad
with no counterparty, so it cannot satisfy the invariant until a reserved `Equity`
account gives it one. The invariant is also awkward to state while `quantity` and
`unit_price` are `DOUBLE PRECISION`: summing float buys and sells does not land on
exactly zero, which is why `qty_is_zero(q) := ABS(q) < 1e-9` already exists. It can
be written against floats with a relative tolerance, but that tolerance then has to
be chosen and justified per call site, and an absolute one is silently
scale-dependent. Exact decimals remove the question instead of answering it
repeatedly; see [0026](0026-exact-decimals-bounded-by-closure.md). Both are
prerequisites of turning the constraint on, so grouping lands first and the
constraint follows.

The read path is unaffected. Holdings, valuation, price coverage and holding
declarations all aggregate `SUM(quantity)` grouped by instrument, and adding a
grouping key changes none of them.
