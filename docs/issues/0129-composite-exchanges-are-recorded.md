---
status: closed
title: Composite exchanges are recorded rather than guessed
milestone: M17
---

An identified instrument should end up with the exchange the provider actually
named, or with nothing plus a record of why -- never with a venue nobody stated.

## Motivation

Provider exchange codes come in two levels and the code reads them as one.
`local/scripts/gen-openfigi-exchange-map.py` builds
`server/plugins/openfigi/exchangemap/codes_generated.go` from the `EQUITY EXCH
CODE` column of the reference CSV. `US` lives in the neighbouring `Composite
Code` column and is absent from the generated map by construction -- it is a
composite covering 20 venue codes across XNYS, XNAS, BATS, EDGE, IEXG, XCIS,
XISX, XCBO, XOTC, OTCM and FINR. Nothing is missing from the source data.

`identifier.USComposite` already sends `US` out as an `exchCode`, so the
codebase knows about composites on the way out and not on the way back.

Three things follow. `resolveExchange` returns `""` for a composite, which is
honest but indistinguishable from an unknown code. `onExchange`
(server/plugins/openfigi/identifier/plugin.go) therefore scores 0 for every US
listing, so the exchange stops contributing to result ranking. And
`resolveResults` is called with `fallbackFirst = true`, so when nothing scores,
result 0 wins -- which is how a bare ticker query settles on an arbitrary
listing. A recorded AAPL mapping response carries around 90 results spanning
every venue in the world, including one whose `exchCode` maps to XWBO.

EODHD has the same shape and fabricates rather than declines. Its map honestly
records `"US": {"XNAS", "XNYS", "OTCM"}` -- the BRK-B cassette shows EODHD
returns `"Exchange":"US"`, so it structurally cannot say which venue -- but
`resolveExchange` takes `mics[0]` and writes XNAS as a canonical `MIC_TICKER`
domain for a NYSE stock. A correct XNYS from another plugin is then discarded as
`discarded_inconsistent`.

The test that would catch this asserts XNAS using Apple, which is genuinely
XNAS, so it passes either way.

## Scope

Ingest the composite namespace as a second map so a composite hint ranks against
any of its venues, and detect a composite result structurally rather than by
lookup -- OpenFIGI sets `figi == compositeFIGI` on the composite row. Stop
`resolveExchange` collapsing a multi-MIC code to the first: leave the exchange
unset and record the composite code as a provider identifier, alongside
`EODHD_EXCH_CODE` and `SEGMENT_MIC_TICKER`. Note `OPENFIGI_COMPOSITE` is already
taken -- it is the composite FIGI, not an exchange.

Partial knowledge is then represented as partial rather than invented: an
instrument known only to trade on some US venue carries the composite code and a
null `exchange_mic`. It reads as "exchange unknown" in the UI, which 0099 is the
neighbour for.

Revisit `fallbackFirst` once the exchange scores again. Taking result 0 when
nothing ranks is what makes a worldwide ticker query arbitrary.

Record the blank-exchange count before and after. It is the baseline the
candidate work in 0131 is measured against.
