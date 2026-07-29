---
status: closed
title: Knowledge-time as-of queries: reproduce a past valuation
dependencies: [0051]
---

Allow a caller to ask what PortfolioDB believed on a given date, not only what
was true on it.

## Motivation

Every `as_of` in the API is valid time. There is no way to reproduce a figure the
system reported last month: `split_factor_at` bounds its product with
`ex_date <= CURRENT_DATE`, prices are forward-filled from whatever is cached now,
and instrument identity is current state. Two identical valuation requests can
legitimately return different numbers with nothing in the response to say why.

That is defensible for a portfolio tracker and is the accepted position in
adr/0016-bitemporal-time-model.md. It stops being defensible once a figure has
been reported to a tax authority or compared against a broker statement, at which
point "why did this change?" needs an answer.

## Resolution

Closed without building knowledge-time as-of queries. Every `as_of` in the API
stays valid time, and a past valuation cannot be reproduced.

The cost is versioned history on every fact that restates -- prices, splits,
instrument identity and transactions. That is four subsystems, each on the
ingestion or valuation path, and transaction ingestion is knowledge-lossy by
replacement (see adr/0002-transaction-ingestion-model.md), so the ingestion model
would have to change as well.

The prerequisites were each declined on their own merits as they came up:
inflation index vintages (0052) and the identity time dimension (0053). What
remained was a single large piece of work whose only justification was this
query, for a system that does not report figures to a tax authority. Rule 8 of
spec/bitemporality.md already states the resulting behaviour -- derived values
are as-of now, and two identical valuation requests may legitimately differ
across days.

If the need arises, the cheaper partial answer is the place to start rather than
versioned history: stamp each valuation response with the wall-clock time it was
computed and the split state it reflects, so that a difference between two runs
is at least explainable even when it is not reproducible.

The reasoning is recorded in adr/0016-bitemporal-time-model.md and the resulting
behaviour in spec/bitemporality.md.
