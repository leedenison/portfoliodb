---
status: open
title: A merge unions listings
milestone: M25
dependencies: [0147, 0149, 0157]
---

A security merge has to merge listing sets -- which nothing in the system does
today. It pairs the loser's lines to the survivor's and then deletes the loser,
so everything on a line the pairing did not carry is destroyed by the cascade.

## Scope

`mergeInstruments` unions listing sets by currency family, per adr/0071. A loser
line whose family the survivor does not hold moves to the survivor as it stands,
which carries its prices, coverage, fetch blocks, dividends, identifiers and
venues with it because the listing id does not change. A line whose family the
survivor does hold merges into it, taking the union of the two. Currency is the
merge key, so the collision case does not arise; adr/0064's rule that a merge
which cannot complete does not proceed extends to listing-grain collisions.

The names that name no line (0157) travel with the security.

`holding_declarations` is repointed, which the merge has never done. Its
`instrument_id` foreign key has no `ON DELETE`, so a merge whose loser carries a
declaration fails outright today.

A line, once it exists, claims the postings that were waiting for it: a fill-in
over `txs.listing_id` matching each posting's own trading currency, rather than a
move off a row. A listing-grain name on no line is claimed the same way, but on
the security's sole line, an unplaced name carrying no currency to match on.
