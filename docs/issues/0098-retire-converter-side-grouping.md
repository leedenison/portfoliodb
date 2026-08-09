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

Grouping stops being an input and becomes derived state, so the user archive should no
longer carry it: it currently lists transactions "and their grouping" as irreplaceable
data. Re-examine against adr/0032-archive-preserves-inputs-not-derived-state.md and
adr/0035-archive-nests-by-aggregate-root.md, and confirm that an archive carrying
evidence alone regroups to the same partition on import.

Fragments left by period replaces before this lands are repaired by the same run, since
the engine works over stored postings rather than over an upload.
