---
status: open
title: Retire converter-side grouping
milestone: M15
dependencies: [0097]
---

Make the server's partition authoritative, stop converters asserting one, and remove
`group_ref`.

## Design

Flip in one step once 0097's shadow run reproduces the converters exactly. The
grouping passes come out of `client/lib/csv/converters/` and the extension's brokers,
leaving them to emit rows and evidence; `Tx.group_ref` and its plumbing go.

## Consequences

Grouping stops being an input and becomes derived state, so the archive stops carrying
it. adr/0043-grouping-does-not-travel-in-the-archive.md settles that and 0084 already
flattened the format for it, so what is left here is deleting the optional `group_ref`
from `archive.v1.Posting` and confirming that an archive carrying evidence alone
regroups to the same partition on import.

Routed residual postings come out of the export with it. They are exported today because
a group exported with its residual sums to zero and `routeResiduals` skips it, which is
an argument about a partition that survives the trip. A residual carries no correlation
evidence, so once the importer regroups there is nothing to say which postings it belongs
with; it is routed fresh after the grouping pass instead.

Fragments left by period replaces before this lands are repaired by the same run, since
the engine works over stored postings rather than over an upload.
