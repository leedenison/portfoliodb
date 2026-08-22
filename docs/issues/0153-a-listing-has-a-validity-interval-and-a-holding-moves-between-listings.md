---
status: open
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
