---
status: closed
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

## Outcome

The currency column is neither derived from the listing nor checked against it.
It stays, because it is the code the amount is quoted in and the family lets that
differ from the code the line is stored under: nineteen pence is not nineteen
pounds. Agreement is a property of the write path, which selects the line from
the stated currency, so no writer can produce a row where the two disagree.

The scope did not anticipate a dividend whose currency matches no line of its
security. A stated currency selects a line and never mints one, so such a payment
is stored nowhere and queued as an `UNATTRIBUTABLE_DIVIDEND`;
`UpsertCashDividends` hands those rows back so an import reports each one as well
as queuing it. That is the opposite trade from a posting, and adr/0073 records
why the two differ.

0157 later deleted the currency-unknown listing this issue's minting rule was
written against, so a security with no line at all gains its first from the
contract rather than having one relabelled.
