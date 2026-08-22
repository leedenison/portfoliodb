---
status: open
title: Corporate events pick their grain
milestone: M25
dependencies: [0146]
---

A split is an action on the security; a dividend is paid in a currency and so
belongs to a listing. Key each on the grain it is a fact about.

## Motivation

`cash_dividends` is keyed `(instrument_id, ex_date)` and carries its own
currency column, so a security-grain dividend collides on its key the first time
one ex-date pays in two currencies. The seam is already in the schema,
unexercised. That currency column is the listing.

## Scope

`cash_dividends` keyed `(listing_id, ex_date)`, its currency column derived from
the listing or checked against it.

`stock_splits`, `corporate_event_coverage` and `corporate_event_fetch_blocks`
stay security-grain. `split_factor_at`, `RecomputeSplitAdjustments` and
`ApplyOptionSplit` take a security and reach its listings, rather than reaching
both grains through one `instrument_id`.

`instruments.underlying_id` becomes `underlying_listing_id`: an option's
deliverable is shares of one line. That makes the currency agreement between an
option and its underlying a check rather than a question, while OCC, OPRA and
FUT_OPT stay security-grain, a contract being cleared in one place.
