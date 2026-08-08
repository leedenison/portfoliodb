---
status: open
title: Carry user preferences in the user archive
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
