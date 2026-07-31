---
status: closed
title: Group transaction legs with tx_groups
milestone: M12
---

Add a `tx_groups` table and a nullable `txs.group_id` so that the legs of one
economic event can be grouped. No balance constraint in this change.

## Motivation

`txs` is already a posting table in all but name: `instrument_id` is the
commodity (currencies are instruments), `quantity` is a signed amount,
`broker` + `account` is the account, and `timestamp` is the date. Holdings are
`SUM(quantity)` grouped by instrument with no type-based sign flip
(server/db/postgres/holdings.go). What is missing is the parent record that
groups the legs of a single event and the invariant that they sum to zero.

The concrete driver is money-weighted return. MWR requires knowing which cash
flows crossed the portfolio boundary (deposits, withdrawals) versus which were
internal (a buy converts cash into shares inside the boundary); TWR needs the
same boundary to sub-period correctly. With single-legged txs that is a
classification problem over the ~22 OFX tx types, per broker, with no
structural guarantee and nothing that fails loudly when a mapping is wrong.
With grouped postings and a small non-asset account vocabulary it is
structural: a flow is external iff it crosses out of the asset accounts.

## Inspiration

Double-entry as implemented by beancount and ledger: a transaction is a set of
postings that must sum to zero.

## Design

- `tx_groups (id, user_id, timestamp, narration, source)`.
- `txs.group_id` nullable FK to `tx_groups`.
- Backfill: each existing tx becomes its own single-posting group.
- No balance constraint here (see 0041) and no ingestion change (see 0038).

The read path is untouched. holdings.go, valuation.go, price_cache.go and
declarations.go all aggregate `SUM(quantity)` grouped by instrument; adding a
grouping key does not change any of them. Cash balance remains `SUM(quantity)`
over currency instruments.

## Note

The decision to adopt double-entry is architectural and should be recorded as
an ADR before this lands. It relates to ADR 0011 (synthetic INITIALIZE
transactions), whose pad has no counterparty today -- see 0037.
