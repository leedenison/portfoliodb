---
status: open
title: Carry the expensive identifier lookup results in the instrument archive
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
server/service/identification/resolve.go. `SerializedInstrument` in
client/lib/json/instruments.ts does not mention it.

**`cik`, `sic_code` and the validity interval are dropped** by the same
serialiser. `cik` and `sic_code` are plugin lookup results;
`valid_from`/`valid_before` records when the instrument was available to trade
and is not derivable.

**`ExportInstrumentsRequest` excludes CASH and FX by default.** That default is
right for browsing and wrong for a rebuild: FX pairs are instruments
(adr/0006-fx-as-synthetic-instruments.md), and an export taken with the default
yields an incomplete set.

## Design

Carry all of it in the instrument part of the admin archive, and give the export
a mode that means everything rather than relying on the browsing default. The
import restores the provider identifiers directly instead of re-deriving them,
so a restored instrument is indistinguishable from a resolved one and no plugin
is called.

Independent of 0082 -- this is what the export carries, not how it is encoded --
but the two are naturally done together.
