---
status: open
title: A listing-grain name that could not be placed names no line
milestone: M25
dependencies: [0147, 0149]
---

The currency-unknown listing is deleted. A listing-grain identifier nobody could
place names its security and no line, in the same nullable column a posting
already uses.

## Motivation

The model carries two representations of "we do not know which line": the
nullable `listing_id` beside `instrument_id` on `txs` and `holding_declarations`
(adr/0072), and the null-currency listing row (adr/0068). Of the eight columns
naming a line, six can never hold an unplaced anything -- prices, coverage, fetch
blocks and dividends are barred by the rule that an unknown listing is not
priceable and not event-bearing, a derivative's underlying by adr/0074, and
`listing_venues` is derived by trigger. The null row exists for exactly two
tables, and about thirty reads step around it.

It is also live wrong. A resolution that identified a security but stated no
currency hands back that security's null listing as the line, so a posting can be
written on one -- which adr/0072 forbids.

See adr/0075-a-name-that-could-not-be-placed-names-no-line.md.

## Scope

`instrument_listing_identifiers` and `provider_listing_identifiers` each gain
`instrument_id`, `listing_id` becomes nullable, and the bare foreign key becomes
the composite `MATCH SIMPLE` pair against `instrument_listings (instrument_id,
id)`. `instrument_listings.currency` becomes `NOT NULL` and
`uq_instrument_listings_unknown` goes with it.

`recompute_listing_venues` skips a row naming no line.
`recompute_instrument_name` reads the identifier's own `instrument_id` rather
than reaching the security through the listing, and loses its `currency IS NULL`
ordering: a name on no line still names the security.

`ensureListing` stops minting for a caller that stated no currency, so a security
may hold no listings and `EnsureInstrument` returns no line -- which is what a
caller stating no currency has named. Placement files an unplaceable name against
the security with a null line rather than erroring.

The reads that filtered the row out stop needing to: about nineteen
`currency IS NULL` tests in SQL and twelve `Currency == nil` tests in Go, plus
`instrument_priced_listing` and `requireCurrencyBearingListing`. `db.Listing.Currency`
stops being a pointer.

An archive's `Listing.currency` becomes required and `Instrument.listings` loses
its minimum of one. The unplaced names ride on the instrument beside the
security-grain ones, because grain alone no longer says where a name belongs.
