---
status: closed
title: Identifiers are stored at their grain
milestone: M25
dependencies: [0146]
---

`identifier.Grain` already declares what every identifier type names. Store the
rows accordingly, so a listing fact learned from a listing-grain identifier
lands on the listing rather than on the security.

## Motivation

Currency and exchange are facts about a listing, and with nowhere to put them
the code has rules that suppress the consequences instead of representing them.
Those rules stop being needed rather than being ported -- porting them
mechanically is the mistake available here. 0144 fixed the identity half of this
against a single-row model; this is the model it was working around.

## Scope

`instrument_listing_identifiers`, with the GiST overlap constraint at listing
grain mirroring `excl_instrument_identifiers_overlap`, and the same split for
`provider_instrument_identifiers`. `identifier.NamesAListing` decides which
table a row lands in.

**Re-read the grain table before using it.** `identifier.Grain` meant security
against venue-listing and now means security against currency line, so the
entries are re-declared rather than carried across. `OPENFIGI_COMPOSITE` and
`SEDOL` move to listing grain -- a composite names a security within a market,
which is a currency line, and SEDOLs are assigned per market and per line. Both
keep `ReassignRare`, so both keep mediating. The `Grain` doc comment also has to
stop implying that listing grain means having a domain: a SEDOL and a composite
FIGI are globally unique without one. Get this wrong and every row of both types
is filed in the wrong table.

That leaves the listing table holding `MIC_TICKER`, `OPENFIGI_TICKER`, `SEDOL`
and `OPENFIGI_COMPOSITE` canonically, and `SEGMENT_MIC_TICKER`,
`EODHD_EXCH_CODE` and the venue FIGI among the provider identifiers.

Resolution returns a security and a listing rather than an instrument:
`server/service/identification/resolve.go`, `server/service/ingestion/resolve.go`,
the four identifier plugins under `server/plugins/` and their cassettes.
`consistentWith`, `fillBlanks` and `confirmedFields` attach currency and venue to
the listing the result named.

A composite exchange code now names a listing exactly, since a country's venues
share a currency, so the ranking work 0129 left open resolves here too. A bare
MIC matching two listings of one security is unresolved and must not be settled
by picking one.

`listing_venues` starts populating, by trigger from the listing-grain
identifiers, in the derived-column pattern `recompute_instrument_name` already
follows. That trigger itself has to change with them: `MIC_TICKER` is ranked
first in the name priority and now lives in the listing table, so it reads both
tables, tie-breaks on `(type priority, currency, domain, value)`, prefers a
listing that has a currency, and fires on listing and listing-identifier changes
as well.

Expect this to run past the PR size guidance; most of it is the plugins and
their cassettes. Re-record a cassette only where the request changed.
