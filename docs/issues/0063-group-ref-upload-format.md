---
status: open
title: Group reference in the standard upload format
milestone: M12
dependencies: [0036]
---

Add an optional `group_ref` to the standard CSV and the `Tx` proto so that an upload
can say which postings are legs of one economic event, and group them on ingestion.

## Motivation

0036 added the group table, but nothing can currently say which rows belong
together. The standard CSV has no grouping column, so every posting arrives as its
own single-posting group no matter what the broker reported.

## Design

- `group_ref` is an opaque string scoped to a single upload. Rows sharing a non-empty
  value are legs of one event; an empty value means the row is its own group. It is
  not a durable identifier and is not stored (see adr/0002-transaction-ingestion-model.md,
  which states transactions have no natural key).
- `ReplaceTxsInPeriod` takes group keys parallel to the txs, resolves each distinct
  non-empty key to one group per upload, and stamps the group with the first leg's
  timestamp.
- `filterStoredTxs` drops non-stored types (e.g. SPLIT) and must carry group keys
  through the surviving indices, so a partially-dropped group still groups correctly.
- `CreateTx` ignores it: one tx, one group.
- No validation rule. A group that does not balance is 0038 and 0041's problem.

Fees are not a column. A fee is a posting with `type=INVEXPENSE`; see
adr/0021-converters-own-transaction-grouping.md.
