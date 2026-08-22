---
status: open
title: A security has listings
milestone: M25
dependencies: [0137]
---

Introduce the level, seed one listing per instrument, and change nothing else.
The invariant this establishes -- every instrument has at least one listing --
is what the rest of M25 leans on.

## Scope

`instrument_listings` and `listing_venues` in `server/migrations/001_initial.sql`
(edited in place; the project takes no new migration files). A listing carries
its instrument, its currency, a half-open `[valid_from, valid_before)` interval
and `created_at`. Uniqueness is on the currency family rather than the raw ISO
code, so a provider quoting the London line in pence and another quoting it in
pounds do not fork one line in two:

    currency_family(code TEXT) RETURNS TEXT IMMUTABLE   -- GBX -> GBP
    UNIQUE (instrument_id, currency_family(currency))   WHERE currency IS NOT NULL
    UNIQUE (instrument_id)                              WHERE currency IS NULL

Two partial indexes rather than `NULLS NOT DISTINCT`, which keeps this off a
Postgres 15 dependency. The listing stores the code it is quoted in, so
`ScaleBars` and `DerivedFXPairs` in `server/pricefetcher/plugin.go` are
untouched and the seeded GBX cash and FX instruments stay as they are. The
family governs this index and nothing else; it never rewrites a currency code.

`GBX -> GBP` is the only member, and the reason to add a table rather than a
literal is that it would be the third statement of that fact -- `DerivedFXPairs`
and `openFIGICurrencyOverrides` in `server/plugins/openfigi/identifier/map.go`
are the other two. Declare it once in a small `server/currency` package (code,
major unit, exponent), derive both maps from it, and hold the SQL function in
lockstep with a test that reads the Go table.

`listing_venues (listing_id, mic REFERENCES exchanges(mic))` is declared here and
populated in 0147, when there are listing-grain identifiers to derive it from.

Go `Listing` and a `ListingDB` interface beside `InstrumentRow` in
`server/db/db.go`; `EnsureInstrument` mints the listing. A `Listing` message and
`Instrument.listings[]` in the proto.

Every instrument gets one listing from `instruments.currency`, null included.
Nothing re-points at a listing yet and `instruments.currency` and
`instruments.exchange_mic` stay where they are until 0155.
