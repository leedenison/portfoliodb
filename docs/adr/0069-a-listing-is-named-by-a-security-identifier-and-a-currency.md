# A listing is named by a security identifier and a currency

`InstrumentRef` names one instrument by a single identifier and is the only way
an archive refers to one. A price group is listing-grain under
[0035](0035-archive-nests-by-aggregate-root.md), so once a security has several
listings the ref must name one of them or prices stop round-tripping. The ref
gains a currency, and a listing is named by the pair.

The alternative was to mint a listing an identifier of its own in the sense of
[0059](0059-an-invented-identifier-round-trips.md). Rejected: an invented
identifier exists to stand in where a source stated nothing that identifies the
thing, and here the archive already holds both halves of what does. A currency
code needs no registry, no round-trip test and no storage.

`bestIdentifierJoin` splits into a security join and a listing join. One join
cannot serve both, and its single priority order already mixed the grains by
ranking `MIC_TICKER` above `ISIN`.

## The security's own name

`recompute_instrument_name` denormalizes `instruments.name` from identifier
priority, and it ranks `MIC_TICKER` first -- a type that is now listing-grain.
The trigger therefore reads both identifier tables, keeps the type priority, and
extends its tie-break to `(type priority, currency, domain, value)` so the
answer is stable across a security's listings. It prefers a listing that has a
currency, so a security with a known line is never named by its unknown one.

**No listing is primary.** Choosing one to name the security would reintroduce
exactly the conflation [0068](0068-a-listing-is-a-currency-of-a-security.md)
removes -- a default listing and an unknown listing are different objects -- and
would buy it for a display label. Dropping tickers from the priority instead
would name most securities by a broker description or a UUID.

The security's `name` is a label rather than an identity: two listings of one
security are told apart by their currency wherever a user has to tell them
apart, not by the name they share.
