---
status: partly superseded by ADR-0055
---

# Instrument resolution order, caching, and eager merge

Amended by [0060](0060-an-identity-claim-is-admitted-by-the-authority-for-its-scope.md),
which requires a merge to act on an association a single plugin result stated
rather than on the identifier set the resolver assembled, and by
[0064](0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md), which says what
happens when a merge cannot complete. The resolution order and the batch cache
stand.

Instrument resolution is ordered to minimize calls to expensive, quota-managed
identifier plugins: (1) DB lookup by `(source, description)` or existing
identifiers, (2) a per-batch cache so the same `(source, description)` resolves
once, and (3) only if still unresolved, call the plugins. The `(source,
description)` identifier is always persisted when a description is resolved so
future uploads with the same source and description resolve by DB lookup and
never re-invoke a plugin. When a client supplies its own external identifiers, no
`(source, NULL, description)` identifier is stored -- the user's identifiers are
authoritative and description-derived mappings would only pollute future lookups.
Client hints (currency, exchange, security type) narrow resolution but are never
stored as canonical data; only plugin/API-confirmed data is authoritative.

When resolution links identifiers that resolve to more than one existing
instrument (e.g. one had only an ISIN, another only a CUSIP), the instruments are
**merged eagerly** during resolution in a single DB transaction: the survivor is
the instrument with more identifiers, ties broken by older `created_at`. Eager
merge keeps the security master converging as data improves; the same logic can
later back a periodic sweep. Because the identifier set an instrument can have is
open-ended (some instruments have no CUSIP, plugin coverage varies), the system
does **not** treat "only one standard identifier known" as an error -- merge on a
shared identifier is what reconciles instruments, once some one result has said
the two identifiers belong together.

## Identity is current state, not a time-varying fact

Superseded by [0055](0055-identifier-validity-is-an-interval.md), which puts a
validity interval on `instrument_identifiers` and makes ticker reuse
representable.

What survives is the merge's own knowledge-loss. The merge keeps no record of the
loser: its canonical fields cascade away along with its prices, splits, dividends
and coverage rows. Those derive from external sources and come back on re-fetch;
the identity judgement does not. Nothing reads merge history -- holdings and
valuation follow from the current instrument set -- and the only consumer would
have been the knowledge-time as-of query, declined in
[0016](0016-bitemporal-time-model.md).
