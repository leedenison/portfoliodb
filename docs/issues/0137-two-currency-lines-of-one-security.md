---
status: open
title: Two currency lines of one security cannot be told apart
---

A security listed on one venue in two currencies -- the GBP and USD lines of the
same iShares ETC, for instance -- may have one ISIN, and PortfolioDB has one
instrument per identifier.

## Motivation

Better hints do not help here. If both lines resolve to the same ISIN, the eager
merge in adr/0004 combines them, and a holding in one currency line becomes
indistinguishable from a holding in the other. Prices then come from whichever
line the price plugin picks, and a portfolio valued in the wrong currency line
is wrong by the FX rate rather than by a rounding error.

This is why the case is out of scope for the candidate work in 0131: it is not a
question of knowing more about the instrument but of the model having no place
to put a second listing.

## Scope

The unscheduled milestone item "Exchange and listing currency: identify and
store per transaction/instrument (and support multiple listings per instrument
if needed)" is this issue. The shape is a listing that carries the venue and the
currency, with the security above it and the transaction naming which listing it
traded on -- and every query that reads `instruments.exchange_mic` or joins
prices by instrument changes with it. 0099 is superseded rather than
neighboured: it argues for one authoritative exchange per instrument, which is
the premise this drops.

Whether the lines share an ISIN is answered: sometimes they do and sometimes
they do not, since legally distinct lines are assigned distinct ISINs. Both
cases suit the same grain -- a shared ISIN is one security with two listings,
distinct ISINs are two securities with one listing each -- and it is the
single-row model that cannot hold the first.

Two decisions follow from that and settle the cost. Every priced thing has a
listing, cash and FX pairs degenerately, and a security whose listing is unknown
carries an empty one, per-security and completed in place as adr/0067 completes
an instrument. Identifiers are security-grain or listing-grain and are never
queried polymorphically: two tables, each with a real foreign key and an
overlap constraint at its own grain. Nothing gives up a capability it would use,
because `identifier.Grain` already declares which level every type names.

Left open: what happens when a security's empty listing turns out to be two real
listings, which is adr/0004's merge one level down; whether a split or a dividend
keys on the security while a price keys on the listing, where `cash_dividends`
already carrying its own currency is evidence the seam is real; and the grain of
`BROKER_DESCRIPTION` and of a broker's contract identifier (0123).

The cost, measured rather than guessed: 14 tables hold a foreign key to
`instruments`, 7 of them in a primary key including the `eod_prices`
hypertable; ~7,000 lines of database Go and 102 SQL literals; 3 of the 5 plugin
interfaces and their 30 cassettes; 24 of 52 RPCs and `InstrumentRef` with them;
12 of 16 spec files and around 6 ADRs. The client is nearly untouched. The
valuation query is close to neutral -- almost everything it keys on
`instrument_id` is already a listing fact, and the two places that read both
grains stay at one join given a view and given that every priced thing has a
listing. P01 is the deadline for the cheap version, since proto and schema
changes are free pre-release.

This wants an ADR before any code.
