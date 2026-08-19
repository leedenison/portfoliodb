---
status: closed
title: Stamp instrument identity from the export date, not the trade date
milestone: M16
---

An option is named in a broker file under the name current at the file's export,
not under the name it wore on the trade date -- the same convention
[0054](../adr/0054-share-count-basis-is-a-convention.md) fixes for share counts,
applied to identifiers. Price and corporate-event imports already pass the
envelope's `exported_at` as the vintage. Transaction ingestion passes the
posting's `trade_date` instead, and `UpsertTxsRequest` carries no vintage at all
for a broker upload to state.

The result is a double adjustment either way round. When the split is already
known, `AdjustOCCForKnownSplits` rebases a symbol the file had already restated
and looks up a strike that never existed, so the stored option is missed and a
second instrument is created for the same contract. When it is not yet known,
the identity is stamped at the trade date and the retroactive pass restates a
name that was already correct.

A broker upload has no envelope, so its vintage is the upload; a user archive
has one and `importTxPart` does not currently see it.

This is [0123](0123-carry-broker-contract-identifiers.md) seen from the other
side: that issue removes the need to rebase by carrying an identifier that does
not move, this one gets the vintage right for the identifiers that do.
