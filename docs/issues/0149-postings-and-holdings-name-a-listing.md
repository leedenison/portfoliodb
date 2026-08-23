---
status: open
title: Postings and holdings name a listing
milestone: M25
dependencies: [0146, 0156]
---

A posting names a security always and the currency line within it when something
said which, so a holding is per line and is valued at the currency it is
actually quoted in.

## Motivation

With prices on the listing and postings on the security, a portfolio holding the
GBP line of a dual-listed security is valued at whichever line a plugin fetched.
`instrument_priced_listing` is the interim bridge 0148 left: it resolves a
security to its line only where the security has exactly one, and reports every
two-line security unpriced.

## Scope

`txs.listing_id`, nullable, beside `instrument_id`, with a composite
`MATCH SIMPLE` foreign key against `instrument_listings (instrument_id, id)` so
the two cannot name different securities, and a CHECK closing the case
`MATCH SIMPLE` leaves open. Null is the posting naming no line -- a first-class
state rather than a sentinel row. See
adr/0072-a-posting-names-a-security-and-a-line.md, which supersedes adr/0070.

`weight_commodity` keeps `inst:<uuid>`. A group's legs have to be weighed at one
grain and the line is not available for every posting, so what a posting balances
in is the security. The residual takes the line every leg it balances shares, and
none where they differ.

The line is settled at ingest, in `server/service/ingestion/`, from what is in
hand: the stated `trading_currency`, then the line identification named, then the
security's sole line where it has one with a currency, then none. Every rung is
find-only -- a broker states a currency to say what its figures are in, so a line
is minted where a provider or a listing-grain identifier asserts one and nowhere
else. `settlement_currency` is not a rung: it is the account's currency.

Holdings and valuation aggregate per line, and a holding on no line reports
unpriced through the path 0148 added. Price fetching makes the opposite trade: a
posting on a known line fetches that line, one on no line fetches every priceable
line of its security.

The merge moves its loser's postings onto the survivor's line of the same
currency family, and onto no line where there is none to match.

Declarations and transfers follow, each in its own change: `holding_declarations`
gains the line in its unique key, and `transfer_matches` keeps its security-grain
key and gains `from_listing_id` and `to_listing_id`, so a transfer between
accounts and a conversion between currency lines are one object. 0153 uses the
second form.
