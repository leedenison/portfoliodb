---
status: closed
title: A listing has a validity interval and a holding moves between listings
milestone: M25
dependencies: [0149, 0150]
---

Listings over time, and the one event this model creates.

## Scope

A listing carries `[valid_from, valid_before)`, following adr/0055 and adr/0018,
and `instruments.valid_from` and `instruments.valid_before` are **deleted**
rather than moved into it, along with their proto fields and the unpopulated
pair on `identifier.Instrument`. Nothing filters or orders on them, no plugin
supplies them, the client shows only identifier validity, and archive import is
their only non-test writer -- they round-trip a value we wrote and decide
nothing. Doing it here rather than in 0155 means the two intervals never
coexist and docs/spec/archive-format.md is edited once.
A delisting closes a listing rather than being represented as a new instrument
and a merge. A redenomination closes one listing and merges into another; GBX to
GBP is not one, since the two are one currency family.

A venue migration is not an event at all under this model -- it is a change to
the set `listing_venues` holds -- and needs nothing here.

A broker converting a holding from one currency line of a security to the other
is a real quantity movement with no economic event behind it. It is
quantity-preserving on one security, which makes it a transfer: it rides the
`from_listing_id` and `to_listing_id` columns 0149 adds to `transfer_matches`,
with its transaction type in docs/spec/tx-types.md and converter support in
`client/lib/csv/converters/`.

Reported as one group, both legs are on one security and balance, so nothing is
routed and the holding moves because the two postings name different lines.
Reported as two, each leaves a `TRANSFER_CLEARING` residual and the pair matches
as any transfer does -- but the netting rule in
`server/db/postgres/valuation.go` has to be suppressed for it. That rule admits a
matched clearing leg so the departure and arrival groups each contribute zero,
which assumes both sides sit on the same commodity at the grain being
partitioned. Valuation partitions by line, so a conversion left to it nets to
zero on the old line and zero on the new, and the holding never moves. Suppress
the netting where `from_listing_id <> to_listing_id`.

## Outcome

Landed in three changes: the lifecycle written down, the deletion, and the
transaction type.

The listing interval needed no schema work. `instrument_listings` has carried
`[valid_from, valid_before)` with its CHECK since 0146, and both protos and the
archive round-trip already carry it, so what this issue owed was the account of
what moves those bounds. adr/0076 records it, and the three specs that each held
part of the model gained the redenomination clause, which until now existed only
in 0137.

The instrument-grain pair was even more inert than the scope claimed. Archive
import stopped being a writer at 0151, when the archive `Instrument` message lost
the fields, so by the time this ran nothing in the tree wrote anything but nil and
there was no value to move rather than a value with nowhere to go.
`docs/spec/archive-format.md` needed no edit at all: it already described a world
with only the listing interval.

`TRANSFER_LISTING` was already in `docs/spec/tx-types.md`, written when the
listing model landed, and in no other file. Registering it turned up two SQL CHECK
constraints enumerating every type name, which the scope did not anticipate and
which would have rejected every posting carrying the value at INSERT.

The rest moved to 0158. No export in hand shows a conversion row, so the converter
half has no shape to implement; and the two-group form cannot pair at all until
the matcher's same-account guard is relaxed, since a conversion happens inside one
account. Landing the netting suppression on its own would have shipped a rule that
no data could reach, which is the objection 0149 raised when it left the
suppression here. 0158 takes the three together.
