---
status: open
title: Export transactions as CSV
---

Export a user's transactions in the standard transaction CSV, so that the format
round-trips and the data can be got back out.

## Motivation

The standard transaction CSV is import-only. `ListTxs` is a paged read for the
transaction list, not an export, so there is no way to take transaction data out
of the system: no backup, no move between instances, and no way to inspect what a
converter actually stored other than through the UI a row at a time.

It also leaves the most heavily used format in the repository as the only one that
is never read back. Prices, corporate events and instrument identities all export
and re-import; every broker converter targets this format and none of them can be
checked by round-tripping their output through storage and out again.

The gap is visible in the format itself. Because nothing writes the file, its
identity columns were designed for what a broker supplies -- a description plus an
optional hint -- rather than for what a writer can guarantee. 0076 hit the same
question for declarations and took the price CSV's shape instead, and the two
should agree.

## Design

Stream the user's own transactions, following `ExportPrices` and
`ExportCorporateEvents` in shape but scoped with `RequireUser` rather than
`RequireAdmin` -- transactions are user data, unlike prices and corporate events,
which are shared.

### Identity

The export writes `identifier_type`, `identifier_value` and `identifier_domain`,
selected with `bestIdentifierJoin` in server/db/postgres/identifier_priority.go,
whose comment requires that every export surfacing a single identifier per
instrument use it so the priority order stays consistent. This is what 0076 does
for declarations.

That leaves the transaction CSV accepting two spellings of the same thing: the
existing `symbol_type` / `symbol` / `exchange_type` / `exchange` hint columns that
converters emit, and the `identifier_*` trio the export writes. Converging on the
latter is the better end state -- `exchange_type` is derivable from
`identifier_type`, since `MIC_TICKER` and `OPENFIGI_TICKER` already say which
system the domain belongs to, so the price CSV's form carries the same information
in one column fewer. Decide whether this issue accepts both and deprecates the
older spelling, or converts the converters. Accepting both first is the cheaper
order, since the import already ignores unknown columns.

### The file has to carry share_count_basis per row

The transaction CSV states the share count only as a file-level
`# share_count_basis=` comment. That is enough for an import, where one file comes
from one source, but not for an export: `txs.share_count_basis` is per row, and a
period can span rows on different bases -- which is exactly what a source that
restates historical rows produces. A single file-level value cannot express that,
and defaulting the rows to their own dates would silently restate any row whose
basis was not its date.

So the export needs a per-row `share_count_basis` column, and the import needs to
read it, with the file-level comment kept as the default for rows that omit it.
This is the same gap 0057 records on the price side.

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
Two consequences to state rather than discover:

- **Group identity is not preserved.** `group_ref` is scoped to an upload and is
  not stored, so the export synthesises one per group and a re-import creates new
  groups with new ids. The postings and their grouping are equivalent; the ids are
  not.
- **Re-import replaces rather than appends**, because bulk upload is
  replace-by-period. Re-importing an export of a period therefore lands on the
  same set rather than doubling it -- which is the useful behaviour, but it depends
  on the period being right, so the export should record the window it covers in
  the file the way the price CSV records coverage.

Routed postings -- `IMBALANCE`, `TRANSFER_CLEARING`, `SOURCE_ROUNDING` -- are
server-generated, but the format already accepts them as `account_type` values and
a group exported with its residual sums to zero, so nothing is routed again on
re-import. Exporting them is therefore idempotent and keeps the file a faithful
picture of what is stored. Confirm that against the balancer rather than assuming
it.
