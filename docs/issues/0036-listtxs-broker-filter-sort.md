---
status: open
title: Add broker filter and sort direction to ListTxs
milestone: M12
---

Add an optional `broker` filter and a `descending` sort direction to
`ListTxsRequest`, so a client can fetch the most recent transaction for one broker
in a single request. The db layer already takes a broker argument that the handler
passes as nil.

Ordering needs a deterministic tiebreaker (the primary key) before a direction can
be exposed: timestamps are not unique and paging is offset-based, so tied rows can
be skipped or repeated across a page boundary. See adr/0015-listtxs-broker-filter.md.

While in the handler, fix the shadowed `err` in the portfolio branch that makes a
`ListTxsByPortfolio` failure return OK with empty results.
