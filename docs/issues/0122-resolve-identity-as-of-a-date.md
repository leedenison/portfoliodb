---
status: open
title: Resolve instrument identity as of a date, not as of now
milestone: M16
dependencies: [0125]
---

Identifier resolution asks providers what a ticker means today, but a portfolio
holds instruments that have since stopped existing. `EA` resolved to a Thai
company once Electronic Arts delisted on 2026-08-05: the live ticker lookup no
longer found it, so the only answer left was another listing of the same symbol.
`ATVI` has been unresolvable since its 2023 merger for the same reason, and both
were held and priced long before either date.

Resolution should be able to ask what a ticker denoted on a given date, and
should prefer identifiers that are not reused over ones that are. A FIGI is
retired and never reassigned; an ISIN is not to be re-used, with documented
national exceptions; a CUSIP is cancelled and eventually reassigned; a ticker is
reused constantly and across venues, which is the one the resolver leans on.

`instrument_listings.valid_from` and `valid_before` already model the interval a
line was tradeable, but nothing writes either except an archive stating them.
Populating them from the providers that report a delisting date is the first
half; consulting them during resolution, alongside a provider lookup that can
see a delisted security, is the second.

Delisting is a lifecycle fact rather than a corporate event, so it belongs in
those columns and not in `unhandled_corporate_events`, and it closes a line
rather than the security above it -- the security has no interval of its own
(adr/0076-a-listing-has-a-lifecycle-and-a-security-does-not.md). A merger is
both: the position terminates, which is a transaction, and the line stops being
tradeable, which is those columns.

See docs/adr/0016-bitemporal-time-model.md and docs/spec/bitemporality.md for the
time model this has to fit.
