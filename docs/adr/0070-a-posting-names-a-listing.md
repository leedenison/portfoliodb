# A posting names a listing

A posting always names a listing and never the security above it, even when the
source did not say which line it traded on -- in which case it names that
security's unknown listing.

The alternative, letting a posting name either grain, fails at the balance
check. `weight_commodity` is `inst:<uuid>` and a group balances on an exact sum
per commodity ([0024](0024-group-balance-is-checked-on-weight.md)), so two legs
stated at two grains are two commodities and the group grows a residual nothing
put there. The weight is computed once at ingest and stored
([0029](0029-posting-weight-is-stored.md)), before resolution has run, so the
grain cannot be deferred until the line is known. `weight_commodity` becomes
`lst:<uuid>` beside the existing `cur:<code>` and `desc:<text>`.

This is affordable only because a listing is a currency
([0068](0068-a-listing-is-a-currency-of-a-security.md)). The discriminator is
already in hand at ingest: `txs.trading_currency` when the source states it,
and the group's cash leg otherwise. A venue-keyed listing would have been
unknown for most rows at the moment the weight is written, which is what made
the mixed-grain posting look unavoidable. A posting's listing is resolved from
the stated trading currency, then the group's cash-leg currency, then a stated
listing-grain identifier including its ticker, then the security's sole listing,
then its unknown listing.

## `txs` carries no security column

`txs` holds `listing_id` alone. Portfolio filters and split adjustment read at
security grain and reach it by joining `instrument_listings`, which is
PK-indexed and sized by the instrument count.

Carrying `instrument_id` alongside would put two columns that can disagree on
the hottest table in the schema -- the failure being removed one level up, at
the point where it is most expensive. The merge already rewrites
`weight_commodity` and the instrument reference in step, and
[0071](0071-listings-merge-by-currency-and-an-unknown-one-splits.md) adds a
second event that must move them together; a third correlated column is a third
chance to drift. The join is not in the per-day valuation loop, which already
keys on facts that are listing facts.

If measurement later shows the join is worth removing, the security column comes
back **derived by trigger**, in the same pattern as `listing_venues`, never
independently written.

## Transfers

`transfer_matches` keeps its security-grain key and gains a listing on each
side. A transfer between accounts and a broker converting a holding from one
currency line to the other are then one object rather than two: both are
quantity-preserving movements of one security with no economic event behind
them, and only the second happens to change listing.
