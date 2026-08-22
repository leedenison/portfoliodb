---
status: open
title: A merge unions listings and an unknown listing splits
milestone: M25
dependencies: [0147, 0149]
---

A security merge has to merge listing sets, and a security whose unknown listing
turns out to be two real ones has to split one -- which nothing in the system
does today.

## Scope

`mergeInstruments` unions listing sets by currency family. Two listings of one
currency merge, taking the union of their venues, identifiers, prices and
dividends; two unknown listings merge into one. Currency is the merge key, so
the collision case does not arise. adr/0064's rule that a merge which cannot
complete does not proceed extends to listing-grain collisions.

Completion in place at listing grain: adr/0067's second form, with its own test
for when a listing is complete. An unknown listing that learns a currency merges
into any sibling already holding it.

The split is tractable because an unknown listing is not priceable and not
event-bearing, so there are no prices, coverage rows, dividends or fetch blocks
to divide -- only postings, each already labelled with its own trading currency.
Each posting moves to the listing its currency names, postings with no currency
stay, and an emptied unknown listing is deleted.
