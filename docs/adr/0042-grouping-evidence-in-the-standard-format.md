---
status: superseded by ADR-0048
---

# Grouping evidence in the standard format

Superseded by [0048](0048-correlations-declare-their-own-semantics.md), which
keeps the shape of the evidence and the token/ordinal split below but replaces
`kind` and `namespace` with a `label` and an explicit declaration of what may be
compared and over what scope. The `role` field described here does not survive:
the problem it was aimed at is that `TxType` is the wrong shape, not that it is
too coarse, and that is settled in
[0044](0044-tx-type-is-declared-and-resolved.md) and
[0045](0045-tx-type-does-not-encode-asset-class.md).

[0041](0041-server-owns-transaction-grouping.md) makes the server the thing that
decides which postings are legs of one event, which means the evidence a converter
reads out of its broker's file has to survive into the standard format in a shape the
server can reason about without knowing the broker.

**A posting carries a repeated correlation element and a role.** A correlation is a
`kind` from a shared vocabulary ([0038](0038-controlled-vocabularies-are-shared.md)), a
`namespace` that scopes what it is comparable with, a `token` that two postings match
on for equality, and an **optional `ordinal`**: the identifier's position in a
monotonic sequence, which is what lets the engine ask how far apart two references are
rather than only whether they are equal.

The ordinal is explicit because proximity is load-bearing and cannot be recovered from
the token. The Fidelity deposit run is held together by references being *near* each
other, not equal, so an equality key alone cannot express it. The converter parses its
broker's identifier into an integer within a declared namespace and the engine compares
the difference.

Inferring the distance from the identifier strings instead -- an edit distance, or any
similar measure -- was rejected. It does not correlate with issuance proximity:
`1000000` and `0999999` are adjacent references four edits apart, while `1000001` and
`2000001` are one edit apart and a million references apart. It would group unrelated
rows and miss related ones, differently for every broker, and it would do so silently.

Where a source's identifiers are genuinely opaque -- an OFX `FITID` is the case in hand
-- the converter emits the correlation with no ordinal, and proximity is simply
unavailable for that broker. That is honest, and preferable to manufacturing an
ordering the source does not have.

`role` is separate from `TxType` because the type is too coarse to drive the rules.
Fidelity's `Cash In Lump Sum`, `Cash In` and `Transfer Into Account` are all
journal-family and play different parts in a deposit run; the type cannot tell them
apart and the grouping rules turn on the difference.

## Considered: a field per broker

Carrying each source's grouping fields verbatim was rejected. The format becomes the
union of every broker's evidence, growing a nullable field per source, until no
consumer can rely on any of it. Abstracting the *shape* of the evidence rather than the
fields keeps the format small and puts the translation where the broker knowledge
already is.

## Relationship to broker_ref

`broker_ref` is the equality half of this, already carried and already stored, and
`counterparty_account` is a pointer of the same kind. Whether they fold into
correlation or stay alongside it is settled when the field lands, not here.
