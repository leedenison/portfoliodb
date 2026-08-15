---
status: closed
title: The Fidelity CSV converter reads columns the download does not carry
milestone: M04
---

The shipped Fidelity CSV converter had come to depend on `Exchange`, `Symbol`
and `Type` -- three columns that exist only in a spreadsheet-preprocessed file,
not in the CSV Fidelity.co.uk hands a user.

## What went wrong

The preprocessed files were sitting in `local/masters/` and were taken for
faithful downloads. Because every column lookup in the converter was optional,
a genuine export still converted and still reported no errors; it just came out
without identification. All 37 security postings in the sample export carried no
`identifierHints` and no `assetClassHint`, leaving them to resolve on the broker
description alone. That is the gap the ETF fix in `333f178` ran into.

Cash rows were unaffected: `Investments == "Cash"` agrees with the spreadsheet's
`Type == "CASH"` on every row of both preprocessed files.

## Resolution

The converter now reads the download and nothing else. All twelve of its columns
are required, and a file missing any of them is refused by name rather than
converted on whatever turned up. Identification comes from the ticker the export
writes in the trailing parentheses of an instrument's description -- the whole of
what it says about a listing -- as a `MIC_TICKER` with no domain, since the
download names no venue. An unlisted fund ends in no ticker and offers no hint.
No asset class is asserted, because the download states none.

`local/scripts/convert-fidelity.py` converts the preprocessed spreadsheet and is
unchanged; that is what it is for.

## Not addressed

A bare ticker is ambiguous across venues, which
[0106](0106-share-a-broker-description-map.md) is about. Recovering an exchange
or an asset class from this file is not possible -- neither is in it.
