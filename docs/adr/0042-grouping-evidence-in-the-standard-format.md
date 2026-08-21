---
status: superseded by ADR-0048
---

# Grouping evidence in the standard format

Superseded by [0048](0048-correlations-declare-their-own-semantics.md), which
carries forward the shape of the evidence, the token/ordinal split and the
rejection of a field per broker, and replaces `kind` and `namespace` with a
`label` and an explicit declaration of what may be compared over what scope.

The `role` field proposed here does not survive. It aimed at `TxType` being too
coarse to drive the grouping rules; the actual problem is that `TxType` is the
wrong shape, settled in [0044](0044-tx-type-is-declared-and-resolved.md) and
[0045](0045-tx-type-does-not-encode-asset-class.md).
