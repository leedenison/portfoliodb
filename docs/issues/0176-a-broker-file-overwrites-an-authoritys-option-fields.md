---
status: closed
title: A broker file overwrites an authority's option fields
milestone: M24
dependencies: [0169]
---

adr/0079 rests on an invariant: nothing user-mediated writes metadata to a
system-authoritative instrument, so the class of an instrument is the provenance of its
metadata and nothing has to be recorded per column to know it. That is what makes 0169
cheap, and it is exact rather than approximate -- as long as it holds.

It holds for `name`, `asset_class`, `cik` and `sic_code`: `replaceUnconfirmedMetadata` is
their only writer and 0169 gates it on the instrument holding no identity of its own.

It does not hold for `strike`, `expiry` and `put_call`. `updateInstrumentOnMatch` sets
all three wherever the caller supplied them, unguarded by the instrument's class, on
every instrument a resolution matched -- and on the ingestion path what it is setting
them from is a broker file. So a contract an identifier plugin described is restated by
whatever the next upload happened to say, on a row that is system-authoritative in every
other respect. It is the failure 0169 closed for the name, in the one place left where
the class stops being the provenance.

The fix is the predicate 0169 already uses, at that write site. Not per-field provenance:
recording an authority per metadata column is the drift 0165 closed for identifier type
knowledge and 0170 is closing for `canonical`, it would need a value per user to be worth
anything, and adr/0079 weighed and declined what it would buy.

`underlying_listing_id` is written by the same function and is already `COALESCE`d, so a
stored value wins and it is not this.

See adr/0079-an-instrument-carries-the-authority-of-the-source-that-named-it.md.
