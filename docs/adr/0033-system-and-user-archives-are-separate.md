# System and user archives are separate

Export and import are split by data ownership rather than bundled into one
archive. The **system archive** carries shared data and no user data: instruments
and their identifiers, prices and coverage, corporate events and coverage,
inflation indices, fetch blocks, unhandled event resolutions and plugin config.
The **user archive** carries a user's own data and no system data: transactions
and their grouping, holding declarations, and preferences.

The two have different owners, different authorisation and different lifecycles.
Shared reference data is curated by an admin and refetchable in principle; user
data is authoritative and recoverable from nowhere. Bundling them would put one
user's transactions in an artefact an admin exports, and force an ordinary user
to hold shared reference data they have no business owning.

## Consequences

User data references system data -- a posting names an instrument -- so restoring
a user archive into an instance whose instruments are not loaded leaves those
postings to resolve from scratch. This is working as intended: the normal
identifier resolution path handles it and the result is correct, merely
expensive. Restoring the system archive first is a **recommendation, not a
constraint**, and it is exactly what tier 2 of
adr/0032-archive-preserves-inputs-not-derived-state.md buys: resolution is the
cost being avoided, not a mechanism being depended on.

Anything spanning the boundary needs an explicit translation rather than an id.
`portfolio_filters.filter_value` already holds an instrument UUID when
`filter_type` is `instrument`, so portfolio definitions cannot be exported as
stored; that is why they are deferred rather than included. Shared portfolios,
which aggregate over several users' data, widen the same seam further.
