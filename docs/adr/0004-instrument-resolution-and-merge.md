# Instrument resolution order, caching, and eager merge

Amended by [0060](0060-an-identity-claim-is-admitted-by-the-authority-for-its-scope.md)
and [0064](0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md). The
resolution order and the batch cache below stand. What the eager merge acts on
does not: merging on the identifiers handed to `EnsureInstrument` merges on a set
the resolver assembled from several plugin results, and 0060 requires an
association a single result actually stated. 0064 adds what happens when a merge
cannot complete. The "Identity is current state" section was already superseded by
[0055](0055-identifier-validity-is-an-interval.md).

Instrument resolution is ordered to minimize calls to expensive, quota-managed
identifier plugins: (1) DB lookup by `(source, description)` or existing
identifiers, (2) a per-batch cache so the same `(source, description)` resolves
once, and (3) only if still unresolved, call the plugins. The `(source,
description)` identifier is always persisted when a description is resolved so
future uploads with the same source and description resolve by DB lookup and
never re-invoke a plugin. When a client supplies its own external identifiers, no
`(source, NULL, description)` identifier is stored — the user's identifiers are
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
does **not** treat "only one standard identifier known" as an error — merge-on-
conflict is what reconciles instruments once a shared identifier appears -- once
some one result has said the two identifiers belong together (0060).

## Identity is current state, not a time-varying fact

Resolution answers "which instrument holds this identifier now", never "which
instrument held it on the transaction date", and the merge keeps no record of
the loser. Giving identity a valid-time dimension was considered and rejected
(see 0053).

No identifier plugin returns instrument validity dates, so
`instruments.valid_from` / `valid_to` are NULL for everything the system
resolves for itself and a resolution filter on them would filter on nothing.
Making ticker reuse representable means a validity interval on
`instrument_identifiers`, which replaces the uniqueness indexes with a GiST
exclusion constraint, gives the name-denormalising trigger a vintage to choose,
and threads an as-of date through every identifier lookup and both batch
resolution caches — the whole of the hottest path in ingestion, for data no
source supplies. Nothing reads the merge history either: holdings and valuation
follow from the current instrument set, and the only consumer would have been the
knowledge-time as-of query, itself declined (0054, and see
0016-bitemporal-time-model.md).

The accepted consequence is that a reused ticker or CUSIP silently rewrites how
every historical transaction that resolved through it is interpreted, and that a
merge destroys the loser's canonical fields along with its cascaded prices,
splits, dividends and coverage rows. Those derive from external sources and come
back on re-fetch; the identity judgement does not.
