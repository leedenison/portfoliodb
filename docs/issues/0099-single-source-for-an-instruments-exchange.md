---
status: closed
title: Single source for an instrument's exchange
---

Closed as superseded by **0137**. That issue drops this one's premise -- that an
instrument has one authoritative exchange -- by making the exchange a set held
against a listing rather than a scalar held against a security, so divergence
between `instruments.exchange_mic` and a `MIC_TICKER` domain stops being
representable rather than being reconciled. The four things this issue wanted to
keep survive on `listing_venues`: the foreign key to `exchanges`, the
`LEFT JOIN exchanges` that populates `Instrument.exchange_info`, the
single-table exchange filter in `ListInstruments`, and one authoritative answer
where several `MIC_TICKER` rows carry different domains. 0155 is where the
column goes.

The composite case this raised is answered rather than left null: a composite
names a country's venues, which share a currency, so it names a listing exactly.

The original text follows.

An instrument's exchange is stored in two places that can disagree. Make one of
them the source and derive the other, so divergence is not representable.

## Motivation

`instruments.exchange_mic` and the `domain` of a `MIC_TICKER` row in
`instrument_identifiers` hold the same value at the same granularity -- both are
run through `normalizeToOperatingMIC` before they are written
(`server/db/postgres/instruments.go`, `InsertInstrumentIdentifier` and
`MergeInstrumentFromArchive`). Nothing enforces that they agree.
`MergeInstrumentFromArchive` writes them independently, each behind its own
`COALESCE`, so a file that states one and not the other leaves the row
internally inconsistent, and a reader gets a different answer depending on which
it consults.

The identifier type is what fixes the namespace of the domain -- ISO 10383 MIC
for `MIC_TICKER`, an OpenFIGI exchange code for `OPENFIGI_TICKER` -- so no
separate discriminator is needed alongside it. This is the same reasoning that
retired the transaction CSV's `exchange_type` column in 0084; the column is gone
but the duplicate storage on `instruments` was not part of that change.

## Design

Preferred resolution: keep `instruments.exchange_mic` as the single authority
for queries and joins, but make it derived rather than independently written --
the pattern `instruments.name` and `instruments.exchange` already follow. The
`recompute_instrument_name` trigger computes it from the `MIC_TICKER` domain,
falling back to the `OPENFIGI_TICKER` domain mapped through
`server/plugins/openfigi/exchangemap`. Callers stop writing it; the archive
stops carrying `exchange_mic` on an instrument, since the identifier set already
says it.

This keeps what the column is worth keeping for:

- the foreign key to `exchanges`, which `domain` (unconstrained `TEXT`) does not
  have;
- `LEFT JOIN exchanges e ON e.mic = i.exchange_mic`, which populates
  `Instrument.exchange_info`;
- the single-table exchange filter in `ListInstruments`;
- one authoritative answer where several `MIC_TICKER` rows carry different
  domains.

The alternative -- drop `exchange_mic` and read the exchange from the
identifiers at every use -- is worth stating and rejecting explicitly in an ADR,
because it loses all four.

## Scope

The derivation rule has to decide two things the current code leaves arbitrary:
which domain wins when an instrument has several `MIC_TICKER` rows (the existing
trigger settles the equivalent `OPENFIGI_TICKER` case with `ORDER BY ii.domain
LIMIT 1`), and what an instrument with no ticker identifier at all gets -- an
`OCC` row carries no domain, so an option's exchange has no source here.

0129 adds a third case. An instrument whose provider named only a composite --
some US venue, without saying which -- carries the composite code as a provider
identifier and no MIC at all, deliberately. The derivation has to leave that null
rather than pick a venue out of the composite, which is the fabrication 0129
removes.

Touches the trigger in `server/migrations/001_initial.sql`, the write paths in
`server/db/postgres/instruments.go`, `server/archiveimport/instruments.go`, and
`Instrument.exchange_mic` in `proto/archive/v1/instruments.proto`.
