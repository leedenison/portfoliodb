---
status: open
title: Two currency lines of one security cannot be told apart
---

A security listed on one venue in two currencies -- the GBP and USD lines of the
same iShares ETC, for instance -- has one ISIN, and PortfolioDB has one
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
if needed)" is this issue. Settle first whether the two lines genuinely share an
ISIN, since that decides whether anything is needed at all. If they do, the
shape is a listing that carries the venue and the currency, with the instrument
above it and the transaction naming which listing it traded on -- and every
query that reads `instruments.exchange_mic` or joins prices by instrument
changes with it. 0099 is the neighbour: it asks where an instrument's exchange
lives, which is the same question one level down.
