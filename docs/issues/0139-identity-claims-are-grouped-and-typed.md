---
status: open
title: Identity claims are grouped and typed
milestone: M24
---

Nothing downstream of resolution can tell an association an identifier plugin
stated from one the resolver assembled. `mergedIds` flattens every result into a
single first-wins-per-type list, so `{ISIN, CUSIP}` reaching `EnsureInstrument`
reads the same whether one plugin returned both or two plugins returned one
each. The first is a claim; the second is a set nobody asserted.

Results must reach the merge site **partitioned by the result that produced
them**. This is not attribution: which plugin answered does not enter the
decision, because every identifier plugin is equally authoritative for a global
identifier. What carries the claim is that the identifiers arrived together, so
the partition is the whole requirement and it is cheaper than recording
provenance per value.

Identifier types also gain the two properties the rules key off -- scope, and
whether the issuer ever reassigns a value. Neither is recorded anywhere;
`identifier_priority.go` is the only per-type table and it ranks names for
export. Reassignment is a declaration made on the evidence available and stored
with it, not a fact, so it needs somewhere revisable to live.

Enabling work for 0140 and 0141, which cannot be attempted before it.

See adr/0060-an-identity-claim-is-admitted-by-the-authority-for-its-scope.md and
adr/0061-transitivity-needs-a-non-reassigned-identifier.md.
