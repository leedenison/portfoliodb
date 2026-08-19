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

`instruments.valid_from` and `valid_before` already model the interval an
instrument was tradeable, and the archive carries them, but nothing writes
either: every instrument in the dev database has both null. Populating them from
the providers that report a delisting date is the first half; consulting them
during resolution, alongside a provider lookup that can see a delisted security,
is the second.

Delisting is an instrument lifecycle fact rather than a corporate event, so it
belongs in these columns and not in `unhandled_corporate_events`. A merger is
both: the position terminates, which is a transaction, and the instrument stops
being tradeable, which is these columns.

See docs/adr/0016-bitemporal-time-model.md and docs/spec/bitemporality.md for the
time model this has to fit.
