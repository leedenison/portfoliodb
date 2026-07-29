---
status: open
title: Knowledge-time as-of queries: reproduce a past valuation
dependencies: [0051, 0052, 0053]
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

## Design

Needs versioned history on the facts that restate -- prices, splits, instrument
identity, and transactions -- which is why it depends on 0051, 0052 and 0053.
Transaction ingestion is knowledge-lossy by replacement
(see adr/0002-transaction-ingestion-model.md), so that model has to be revisited
too.

A cheaper partial answer worth considering first: stamp each valuation response
with the wall-clock time it was computed and the split state it reflects, so that
a difference between two runs is at least explainable even when it is not
reproducible.

See docs/spec/bitemporality.md.
