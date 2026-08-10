---
status: open
title: Carry grouping evidence in the standard format
milestone: M15
dependencies: [0092]
---

Give a posting a broker-neutral description of why it might belong with another
posting, and make each converter synthesise its broker's data into it.

## Motivation

adr/0041-server-owns-transaction-grouping.md moves the grouping decision to the
server. The server cannot make it on what the standard format carries today: the
converter's answer arrives as `group_ref`, which is opaque, upload-scoped and not
stored, and the evidence behind it never leaves the converter at all.

## Design

The shape is settled in adr/0042-grouping-evidence-in-the-standard-format.md: a
repeated correlation on `Tx` carrying `kind`, `namespace`, `token` and an optional
`ordinal`, plus a `role` that says what part the row plays where `TxType` is too
coarse to. `kind` and `role` are controlled vocabularies and belong in
`proto/type/v1/type.proto` with the rest (0092, adr/0038-controlled-vocabularies-are-shared.md).

The ordinal is the part that needs care. It is a monotonic integer within its
namespace, so `abs(a - b)` is a count of references and comparable across uploads --
not a rank within one file, which would not be. A converter whose broker issues opaque
identifiers emits no ordinal.

Per converter:

- **Fidelity CSV and the extension's Fidelity JSON** already parse `referenceId` as a
  number and use its distance when grouping. That becomes the ordinal.
- **OFX** has `FITID` on every transaction and no ordering, so it emits an equality
  token only.

`broker_ref` and `counterparty_account` are stored already and overlap this: the
first is the equality half, the second a pointer of the same kind. Decide here whether
they fold into correlation or stay alongside it.

## Scope

The evidence has to be stored on `txs` and to travel in the user archive, or a rebuild
from an archive would have nothing to group on. It lands on the same flat `Posting` the
archive already carries in both directions after 0084, so there is no structural change
to the format here -- only fields. Converters populate it while still emitting
`group_ref`; nothing reads it until 0097.
