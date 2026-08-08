---
status: closed
title: Carry cash dividends in the corporate event archive
dependencies: [0078]
---

Include cash dividends in the corporate event part of the admin archive.

## Motivation

`cash_dividends` is a stored entity with API support, but the file format never
carried it -- docs/spec/csv-format.md says so outright: "Cash dividends are part
of the API but are not yet carried by this file format." Stock splits export and
re-import; dividends do not, so a rebuild loses every one of them and has to
refetch from a provider that may no longer offer the history.

## Design

A second event kind alongside splits in the corporate event part, grouped by
instrument like the rest (adr/0035-archive-nests-by-aggregate-root.md).

Two things to settle rather than assume. Dividends carry knowledge time the way
splits do (docs/spec/corporate-events.md,
adr/0005-corporate-events-design.md), so `first_known_at` needs the same
backwards-only treatment on import. And coverage is stored per (instrument,
plugin) for corporate events generally; whether dividend coverage is tracked
separately from split coverage decides whether the archive needs one coverage
set or two.

Closed by 0082. Moving corporate events onto the archive schema carried
dividends as a matter of course: the export query and the ingestion worker
already handled them end to end, and only the client-side `splitsToJson` filter
dropped them, so excluding them would have meant adding a filter on the way out
and a refusal on the way back in.

Both open questions are settled. Dividends carry `first_known_at` with the same
backwards-only treatment as splits, which the e2e round trip asserts. And there
is one coverage set rather than two, because `corporate_event_coverage` is keyed
`(instrument_id, plugin_id, covered_from)` and has no event-kind dimension: a
span records that a provider was asked about those dates, not which kind of
event it was asked about.
