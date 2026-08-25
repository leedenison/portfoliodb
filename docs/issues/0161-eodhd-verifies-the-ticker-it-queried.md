---
status: open
title: EODHD verifies the ticker it queried
milestone: M25
dependencies: []
---

The EODHD identifier plugin searches on the best identifier it was given and
verifies the answer against the query -- for an ISIN. A ticker query is not
verified at all.

The Search API is fuzzy and matches on the name as readily as on the code, which
is why the ISIN check exists. Nothing makes a ticker query different: a search
for one symbol answers with another company's listing, `bestMatch` takes the
first primary common stock in the response, and the plugin returns a whole
answer -- a name, a currency, a venue and that security's ISIN -- as though it
were about the symbol that was asked for.

What that answer then does is not confined to this plugin. It is one result
among several the resolver compares, so it reaches merge admission and, where
nothing there contradicts it, the security a resolution landed on.

Found while working 0160, and independent of it: the guard 0160 adds is about
what two results may contribute to each other, and this is about one result
being about what it claims.

## Scope

Verify a ticker query against `Code` the way an ISIN query is verified against
`ISIN`, and return `ErrNotIdentified` where they differ. Both sides normalize
their class separator first: the query carries EODHD's dash and a provider
writes the separator however it likes, so one symbol must not read as two.
