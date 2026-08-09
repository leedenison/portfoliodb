---
status: closed
title: Carry user preferences in the user archive
milestone: M14
dependencies: [0078]
---

Export and import a user's display currency and ignored asset classes.

## Motivation

Both are settings the user chose and nothing can recover: `users.display_currency`
and the `ignored_asset_classes` rows. They are small, but a rebuild that silently
resets a user's display currency to the `USD` column default and re-ingests the
transaction types they had chosen to ignore is a rebuild that changed their data.

`GetDisplayCurrency` / `SetDisplayCurrency` and `GetIgnoredAssetClasses` /
`SetIgnoredAssetClasses` already exist, so this is the archive part rather than
new API surface.

## Design

A small part of the user archive
(adr/0033-system-and-user-archives-are-separate.md). Preferences are the only
user-owned data with no reference to anything the system archive owns, so they
restore cleanly whatever order the parts arrive in.

Note that changing ignored asset classes affects what future ingestion keeps, so
restoring preferences before transactions and restoring them after are not the
same thing. Import order within the user archive should be settled here.

Closed. Preferences travel in the user archive, and the whole user-archive spine
landed with them: ArchivePart values for the user parts, an export and an import
RPC, a `user_archive` job type, `processUserImport`, and the `/archive` page. A
part with no RPC, no worker case and no page is not testable end to end, so the
smallest part carried the machinery the rest will reuse.

Four things settled here.

Import order is preferences first, and the reason is now in
docs/spec/archive-format.md rather than open: which asset classes are ignored
changes what a later transaction import keeps. `ArchivePart` numbers its values
in two blocks, one per archive, because no part belongs to both documents, and an
export naming a part from the other block is refused rather than dropped.

The part's unit is a setting rather than a row, so its total is how many settings
the file states and a rejected setting carries a row index of -1.

One unusable ignore rule rejects the whole `ignored_asset_classes` setting rather
than that rule. Setting the rules replaces the set and deletes the transactions
and declarations it covers, so applying the rules the reader could read would
delete on the strength of a file it could not read.

A restored display currency fires the price trigger once per import, as
`SetDisplayCurrency` does per call, so a rebuilt instance is not left without FX
rates for its own display currency until the next scheduled cycle.
