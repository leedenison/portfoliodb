---
status: open
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
