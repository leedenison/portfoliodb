# Instrument resolution order, caching, and eager merge

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
conflict is what reconciles instruments once a shared identifier appears.
