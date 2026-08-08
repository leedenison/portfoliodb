---
status: open
title: Carry holding declarations in the user archive
milestone: M14
dependencies: [0078]
---

Export and import a user's holding declarations as part of the user archive, so
that pads and assertions survive a rebuild and can be restated in bulk.

## Motivation

A declaration is the user's own statement about a holding. Unlike a transaction
it is not recoverable from a broker, and unlike a price it cannot be refetched
from anywhere -- it is tier 1 in
adr/0032-archive-preserves-inputs-not-derived-state.md, and nothing exports it
today. A rebuild currently loses every pad and every assertion.

Bulk restatement matters as much as the rebuild. An assertion is checked against
what the transactions add up to, and it is the only thing in the system that
catches a misparsed broker file, a missed transfer or a converter that silently
dropped a row (adr/0030-declarations-are-padded-then-asserted.md). Entering them
one form at a time does not scale to a statement covering several dozen
holdings.

Reconciling a statement against the system is **not** part of this issue. 0043
already checks assertions against the computed holding and surfaces the gap, so
that belongs in the UI rather than in a file the user diffs by hand. Bulk entry
at inception is split out into 0088.

## Design

Declarations are user data, so they sit in the user archive and never in the
system one (adr/0033-system-and-user-archives-are-separate.md). The group is the
statement -- one account, one `as_of_date` -- with the declared holdings as its
rows, per adr/0035-archive-nests-by-aggregate-root.md.

Each row needs the instrument identity, the declared quantity as an exact
decimal, and `share_count_basis`, which defaults to the group's `as_of_date` as
the table trigger does. Once 0075 lands it also carries `declared_cost` and
`cost_currency`; specify the shape with them in mind so adding them is a new
optional field rather than a second format.

`broker` and `account` come from the file rather than the request, since an
archive has to be self-describing.

### Identity

The export writes the identifier selected by `bestIdentifierJoin` in
server/db/postgres/identifier_priority.go, whose comment requires that every
export surfacing a single identifier per instrument use it so the priority order
stays consistent.

Two consequences follow from identity being current state rather than a
time-varying fact (adr/0004-instrument-resolution-and-merge.md). A row can fail
to resolve on import even though the instrument was picked in the UI when the
declaration was created, if the priority order lands on an identifier the
resolution path cannot take back; in this flow that is a defect rather than user
error and should fail loudly. And a merge between export and import can move an
identifier to a different instrument, so a file re-imported across one lands
elsewhere. That is the same class of problem as 0053 and is not solved here.

### An unresolved declaration has nothing to attach to

A transaction that fails to identify is still an event that happened, so the
system keeps it and surfaces the failure. A declaration that fails to identify is
a statement about a holding the system cannot name -- there is nothing for it to
pad and nothing to check it against -- so it is likely better rejected as a row
error than stored against an unresolved instrument. Settle which, and surface
failures the way transaction uploads do.

### Re-importing an exported file must be a no-op

`UNIQUE (user_id, broker, account, instrument_id, as_of_date)` means a re-import
collides on every unchanged row. The declaration API answers that with
`ALREADY_EXISTS`, which is right for a user creating one by hand and wrong here:
it would fail the restore this issue exists for.

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

## Plumbing

`ListHoldingDeclarations`, `CreateHoldingDeclaration` and
`UpdateHoldingDeclaration` already exist, as does the Opening Balances tab. The
export and import controls live on the consolidated user page from 0080 rather
than on that tab.
