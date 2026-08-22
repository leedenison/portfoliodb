---
status: open
title: Prices and price fetching are listing-grain
milestone: M25
dependencies: [0146]
---

A price is quoted in a currency, so it belongs to a listing. This is the change
0137 exists for: with prices on the security, a portfolio holding the GBP line is
valued at whichever line the plugin fetched.

## Scope

`eod_prices` keyed `(listing_id, price_date)`, which means recreating the
hypertable, plus `price_coverage`, `price_fetch_blocks`, the
`merged_price_coverage` view and the valuation query in
`server/db/postgres/valuation.go`.

`pricefetcher.Plugin` eligibility moves to the listing: `AcceptableCurrencies`
tests the listing's currency and `AcceptableExchanges` its venue set, so the
fetch unit and the key agree and `price_fetch_blocks` neither loses a line the
provider carries nor re-asks for every line of one it does not.

A currency-unknown listing is not priceable. The fetcher skips it and its
holding reports through the existing unpriced path rather than acquiring prices
whose currency nobody could state.

Delete the `COALESCE(inst.currency, $4)` shortcut in the valuation query, which
treats a null currency as the display currency and so values the holding at an
implied FX rate of 1. **This moves valuation totals**, so count the affected
rows -- non-cash instruments holding a null currency -- before and after. Cash
never reaches the branch, always resolving through a `CURRENCY` identifier.
Retire the matching rule in docs/spec/display-currency.md.

Keep the valuation query simple: prefer a view in `001_initial.sql` and justify
anything that does land in `valuation.go`. The query should come out close to
neutral, since almost everything it keys on `instrument_id` is already a listing
fact.

Expect this to run past the PR size guidance; the bulk is mechanical
re-pointing across roughly a hundred SQL literals.
