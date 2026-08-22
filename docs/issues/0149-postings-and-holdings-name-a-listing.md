---
status: open
title: Postings and holdings name a listing
milestone: M25
dependencies: [0146]
---

A posting names a listing and never a security, so the balance check never sees
two grains.

## Motivation

The alternative -- letting a posting name the security when the broker did not
say which line -- fails first at the balance check. `weight_commodity` is
`inst:<uuid>` and a group balances on an exact sum per commodity, so two legs
stated at two grains are two commodities and the group grows a spurious
residual. It is decided at ingest, before identification runs.

Nothing has to be decided late, because the discriminator is already there:
`txs.trading_currency` exists and a group's cash leg carries a currency
regardless.

## Scope

`txs.listing_id`, and `weight_commodity` becomes `lst:<uuid>` beside the
existing `cur:<code>` and `desc:<text>`. The merge in
`server/db/postgres/instruments.go` remains the only thing that rewrites these
after ingest.

`txs` gains no denormalised `instrument_id`. `portfolio_matched_txs` and the
split-adjustment path reach the security by joining `instrument_listings`, which
is PK-indexed and sized by the instrument count, and neither is in the per-day
valuation loop. Two columns that can disagree on the largest table in the schema
is the failure being removed one level up. If measurement later justifies it,
the column returns derived by trigger, as `listing_venues` is, never
independently written.

Listing resolution at ingest, in `server/service/ingestion/`: the stated
`trading_currency`, then the group's cash-leg currency, then a stated
listing-grain identifier including its ticker, then the security's sole listing,
then its currency-unknown listing.

Holdings aggregate by listing. `transfer_matches` keeps its security-grain key
and gains `from_listing_id` and `to_listing_id`, so a transfer between accounts
and a conversion between currency lines are one object; 0153 uses the second
form. `holding_declarations` gains the listing in its unique key. Lots key on
the acquiring posting and are unchanged.
