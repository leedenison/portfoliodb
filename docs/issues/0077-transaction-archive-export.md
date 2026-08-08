---
status: open
title: Carry transactions in the user archive
milestone: M14
dependencies: [0078]
---

Export a user's transactions as part of the user archive, so that the data can
be got back out and restored on a rebuilt instance.

## Motivation

Transactions are import-only today. `ListTxs` is a paged read for the
transaction list, not an export, so there is no way to take the data out: no
rebuild, no move between instances, and no way to inspect what a converter
actually stored other than through the UI a row at a time.

They are tier 1 in adr/0032-archive-preserves-inputs-not-derived-state.md.
Replaying the original broker files is not an equivalent, because replay is not
idempotent across versions -- converters change, so the same file can produce
different postings -- and it re-runs identification, which is the expensive
operation the archive exists to avoid.

## Design

Stream the user's own transactions, following `ExportPrices` and
`ExportCorporateEvents` in shape but scoped with `RequireUser` rather than
`RequireAdmin`. Transactions are user data, so they sit in the user archive and
never in the system one (adr/0033-system-and-user-archives-are-separate.md).

### Grouping is structural

The group is the aggregate root, per
adr/0035-archive-nests-by-aggregate-root.md: postings nest inside a tx group
rather than being flat rows tied together by a key. The group carries no id.

This matters because grouping is not regenerable.
adr/0021-converters-own-transaction-grouping.md makes it the converter's job and
the server explicitly does not pair rows or infer a missing leg, so no rule
exists that could rebuild it from postings alone. An export that dropped it
would lose the balance invariant, residual attribution through
`ListResidualBalances`, and the association between a fee and the trade it
belongs to.

Nesting also removes the problem a flat file would have had. There is no
`group_ref` to synthesise, and `tx_groups.id`, `job_id` and `created_at` are
generated and simply not written.

`tx_groups.timestamp` is dropped too. It cannot diverge from its postings:
`insertPostings` in server/db/postgres/txs.go creates the group from the first
leg that names it, using that leg's timestamp for both rows, and no other path
writes the column. It is derived, so 0078 gives `TxGroup` no timestamp field.

### Identity

Each posting carries the identifier selected by `bestIdentifierJoin` in
server/db/postgres/identifier_priority.go, whose comment requires that every
export surfacing a single identifier per instrument use it so the priority order
stays consistent. `Tx.identifier_hints` is already a repeated
`InstrumentIdentifier`, so nothing on the API changes.

The standard transaction CSV's `symbol_type` / `symbol` / `exchange_type` /
`exchange` columns are **not** renamed to match. They are deleted along with the
rest of the format when the import moves to the archive schema under 0084, and
renaming columns that are about to be removed would touch `convert-fidelity.py`,
`convert-ibkr.py`, `convert-schwab.py`, the masters in `local/standard-format/`
and the e2e fixtures twice over.

### What is not exported

- **The derived pair.** `split_adjusted_quantity` and `split_adjusted_unit_price`
  are a recomputable cache carrying a rounding
  (adr/0026-exact-decimals-bounded-by-closure.md). The export writes the raw
  `quantity` and `unit_price`, which are exact, and the import recomputes the
  cache. Emitting both would also invite mixing share counts, which
  docs/spec/bitemporality.md rule 4 forbids.
- **`weight` and `weight_commodity`**, which are computed at ingest from the raw
  columns and the tx type (adr/0024-group-balance-is-checked-on-weight.md).
- **Synthetic INITIALIZE postings and their `EQUITY` counterparties.** These are
  derived from holding declarations, are excluded from replace-by-period, and are
  exported by 0076 as the declarations they come from. Emitting them here would
  re-import a pad as a real transaction and collide with the partial unique index
  that allows one per holding per account type.

### Round trip

Transactions have no natural key (adr/0002-transaction-ingestion-model.md), so an
exported file does not re-import to the same rows in the way a price file does.
Re-import replaces rather than appends, because bulk upload is
replace-by-period, so re-importing an export of a period lands on the same set
rather than doubling it. That depends on the period being right, so the archive
records the window it covers.

Routed postings -- `IMBALANCE`, `TRANSFER_CLEARING`, `SOURCE_ROUNDING` -- are
server-generated, but the format accepts them as `account_type` values and a
group exported with its residual sums to zero, so nothing is routed again on
re-import. Exporting them is therefore idempotent and keeps the file a faithful
picture of what is stored. Confirm that against the balancer rather than assuming
it.
