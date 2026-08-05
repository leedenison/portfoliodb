---
status: open
title: Import and export holding declarations as CSV
---

Let a user import and export their holding declarations as a CSV, so that pads
and assertions can be entered in bulk and edited outside the application.

## Motivation

Declarations are created one at a time through a form. That is workable for a pad
-- a user seeds an opening balance once -- but it is the wrong shape for the half
of the mechanism that carries the safety.

An assertion is checked against what the transactions add up to, and it is the
only thing in the system that catches a misparsed broker CSV, a missed transfer or
a converter that silently dropped a row
(adr/0030-declarations-are-padded-then-asserted.md). To catch anything it has to
be entered, and the natural source is a statement: one date, every holding in the
account, several dozen rows. A user who has to fill in a form per holding per
statement will do it once and never again, so the assertion half of pad-and-assert
goes unused and the pad -- which is true by construction and can never catch an
error -- is all that is left.

The same argument applies to the pad at inception. A portfolio with forty holdings
is forty forms before the opening balances are right.

Export matters as much as import, for three reasons. It is what makes editing
practical: pull the current declarations, reconcile them against a statement in a
spreadsheet, put them back. It is the only way to get the data out, and a
declaration is a user's own statement rather than something recoverable from a
broker. And it is what makes the round trip testable.

## Design

A CSV, following the conventions the other import formats already share -- header
names case-insensitive, `#` comment metadata, extra columns ignored. See
docs/spec/csv-format.md, which gains a fourth format alongside the transaction
CSV, the price CSV and the corporate event JSON.

Columns, with `broker` supplied by the upload request as it is for transactions:

| Column                                            | Required | Notes                                              |
| ------------------------------------------------- | -------- | -------------------------------------------------- |
| `account`                                         | Yes      | The broker account the holding is in.               |
| `instrument_description`                          | Yes      | As for a transaction row.                           |
| `declared_qty`                                    | Yes      | Exact decimal. May be negative -- a short at inception is permitted. |
| `as_of_date`                                      | Yes      | The date the declaration refers to.                 |
| `share_count_basis`                               | No       | Defaults to `as_of_date`, as the table trigger does. |
| `symbol_type`, `symbol`, `exchange_type`, `exchange` | No    | Identifier hints, exactly as the transaction CSV defines them. |

Once 0075 lands the format also carries `declared_cost` and `cost_currency`. The
format should be specified with them in mind even if it ships first, so that
adding them is a new optional column rather than a second format.

### Instrument identity is the hard part

A declaration references `instrument_id`, which is a UUID and cannot appear in a
user-facing file. Resolution therefore goes through the same identification path a
transaction upload uses: the description plus an optional identifier hint, through
the identifier plugins.

That has a consequence worth deciding rather than discovering. A transaction that
fails to identify is still an event that happened, and the system keeps it and
surfaces the failure. A declaration that fails to identify is a statement about a
holding the system cannot name -- there is nothing for it to pad and nothing to
check it against -- so it is likely better rejected as a row error than stored
against an unresolved instrument. Settle which, and surface failures the way
transaction uploads do.

### Re-importing an exported file must be a no-op

`UNIQUE (user_id, broker, account, instrument_id, as_of_date)` means a re-import
collides on every unchanged row. The declaration API answers that with
`ALREADY_EXISTS`, which is right for a user creating one by hand and wrong here:
it would fail the export-edit-import loop that is the point of the feature.

Import is an upsert on that key. A row whose quantity has changed restates the
declaration; a row identical to what is stored changes nothing.

### Absence is not deletion

A bulk transaction upload replaces a period, so a row missing from the file is
removed (adr/0002-transaction-ingestion-model.md). Declarations do not work that
way and the asymmetry should be explicit, because it is the natural assumption to
make. A declaration missing from an imported file is left alone; deleting one
stays an explicit action. A file assembled from one statement covers one date and
one account, and treating everything outside it as retracted would delete the
user's other checkpoints.

### The file carries no kind column

Pad and assertion are derived from the declaration dates, not stored
(adr/0030-declarations-are-padded-then-asserted.md). An export may show which is
which for the reader's benefit, but importing it would be a second statement of
what `as_of_date` already says, and the two could disagree.

### Export

Streaming, following `ExportInstruments` and `ExportPrices`. It exports the user's
own declarations only.

## Plumbing

`ListHoldingDeclarations`, `CreateHoldingDeclaration` and
`UpdateHoldingDeclaration` already exist, as does the Opening Balances tab that
would host the upload and download controls. The CSV upload UI from 0012 is the
pattern for the import side.
