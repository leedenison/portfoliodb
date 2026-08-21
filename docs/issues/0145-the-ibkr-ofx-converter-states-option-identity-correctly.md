---
status: open
title: The IBKR OFX converter states option identity correctly
milestone: M16
---

`convertIbkrOfx` pushes the SECLIST `TICKER` of any CONID-typed security as an
`OCC` identifier without checking that it is one. For the Eurex contracts in the
sample statements it is not: `P RHM  20250919 560 M` is IBKR's own rendering,
and no OCC symbol exists for a contract OCC does not list. The fix for those is
to emit no OCC at all rather than to build one, which leaves them with the
broker description until 0123 gives them a contract identifier.

The file has what a real OCC needs and the parser throws it away. `buildSecList`
reads `SECNAME`, `TICKER` and `SECID` and drops `OPTTYPE`, `STRIKEPRICE`,
`DTEXPIRE` and `SHPERCTRCT`, so the symbol can be constructed and checked rather
than trusted, and the contract multiplier stops being inferred from the presence
of an OCC hint.

The join is also wrong. The parser does not thread a posting's own `SECID` out
of `parseOfxStatement`, so the converter re-derives which security a posting
belongs to by matching `secName` against the instrument description as strings.
Carrying the `SECID` through replaces that with the identifier the posting
actually stated, and is the same plumbing 0123 needs.
