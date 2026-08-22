---
status: open
title: An archive names a listing
milestone: M25
dependencies: [0147, 0148, 0149, 0150]
---

Until an archive can name a listing, prices do not round trip.

## Scope

`InstrumentRef` names one instrument by a single identifier and is the only way
an archive refers to one. It gains a currency, so a listing is named by a
security identifier and a currency and no identifier has to be invented for one.

`bestIdentifierJoin` splits into a security join and a listing join. Its single
priority order currently ranks `MIC_TICKER` above `ISIN` and so already mixes
the grains; two orders is the fix.

`PriceGroup` and `Declaration` name listings. `CorporateEventGroup`,
`FetchBlockGroup` and `UnhandledEventGroup` stay per instrument, because
coverage, splits and unhandled events are facts about the security; the currency
moves on to the row that varies by it -- a `CashDividend` already carries one,
and a `PRICE` fetch block gains one -- under adr/0035's rule about which level a
field belongs at. The archive `Instrument` record carries `listings[]` and drops
`currency`, `exchange_mic` and its validity interval, all of which the listing
now holds. `server/archiveimport/instruments.go` resolves the pair on import.

Amends adr/0035. Proved by `e2e/system-archive-roundtrip.spec.ts` and
`e2e/price-import-merge.spec.ts` over a fixture holding a two-listing security.
