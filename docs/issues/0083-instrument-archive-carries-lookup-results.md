---
status: closed
title: Carry the expensive identifier lookup results in the instrument archive
milestone: M14
---

Export everything identifier resolution produced, so a rebuild does not pay for
it a second time.

## Motivation

Not re-running identifier lookup is the reason the archive exists. Lookups are
paid, rate-limited and slow, and the results are tier 2 in
adr/0032-archive-preserves-inputs-not-derived-state.md. Three gaps mean a
rebuild re-runs them anyway.

**`provider_instrument_identifiers` is exported by nothing.** It is the recorded
output of the lookups themselves -- provider-scoped identifiers such as
`EODHD_EXCH_CODE` and `FIGI` -- written by all three identifier plugins
(server/plugins/openfigi/identifier/map.go,
server/plugins/eodhd/identifier/map.go,
server/plugins/massive/identifier/map.go) and saved at
server/service/identification/resolve.go. `ListInstrumentsForExport` loads it
onto the row and `archiveInstrument` in server/service/api/instruments.go does
not write it out.

**`cik`, `sic_code` and the validity interval were dropped** by the
hand-written serialiser that preceded the archive. 0082 closed that half.

**`ExportInstrumentsRequest` excludes CASH and FX by default.** That default is
right for browsing and wrong for a rebuild: FX pairs are instruments
(adr/0006-fx-as-synthetic-instruments.md), and an export taken with the default
yields an incomplete set.

## Design

Carry all of it in the instrument part of the system archive, and give the export
a mode that means everything rather than relying on the browsing default. The
import restores the provider identifiers directly instead of re-deriving them,
so a restored instrument is indistinguishable from a resolved one and no plugin
is called.

Independent of 0082 -- this is what the export carries, not how it is encoded --
but the two are naturally done together.

0082 has since landed the widened export query, so `cik`, `sic_code` and the
validity interval already reach the file. What remains here is
`provider_instrument_identifiers` -- carried on export and restored on import
without calling a plugin -- and the export mode that means everything rather
than the browsing default that excludes CASH and FX.

A third gap in the same query, found while building 0079's page: the default
branch is `asset_class NOT IN ('CASH', 'FX')`, and an instrument whose
`asset_class` is NULL fails that predicate rather than passing it, because
`NULL NOT IN (...)` is NULL and not TRUE. An instrument created by a price
import before identification has assigned it an asset class is therefore
dropped from every export -- which is exactly the instrument a rebuild cannot
reconstruct. The export mode that means everything fixes this case too, but a
NULL asset class should pass the browsing default as well: nothing intends to
hide an unidentified instrument from an export.

Closed. The instrument part carries `provider_identifiers`, and the import
restores them without calling a plugin.

Two things landed differently from the description. There is no new export mode:
an export with no asset-class filter now means every instrument rather than the
browsing default, so CASH, FX and an instrument whose `asset_class` is still NULL
all come out, and the `NULL NOT IN (...)` case disappears with the predicate that
caused it. `exchange` and `asset_classes` stay as the opt-in filters 0017 will
build on.

And a third gap turned up in the import rather than the export. Exporting CASH
and FX makes a collision the ordinary case, since migration 002 seeds those rows
on every instance. `EnsureInstrument` matches rather than duplicates, but a match
set the underlying and the option terms and nothing else, so everything
resolution had added to a seeded row was dropped on import.
`MergeInstrumentFromArchive` now fills the gaps a match leaves -- identifiers the
row lacks and columns still NULL -- and never overwrites, so an import cannot
rewrite reference data the instance already had.
