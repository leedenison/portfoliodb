---
status: closed
title: Store price coverage rather than inferring it from row presence
---

Price coverage was inferred: `PriceCoverage` was `range_agg` over `eod_prices`,
so a covered range and one that happened to have rows were the same thing. That
left no way to record a negative answer, and the fetcher dropped the ones it got
-- `ErrNoData` and gaps outside a plugin's `max_history_days` recorded nothing,
so a delisted or pre-IPO range was rediscovered as a gap and refetched on every
cycle for ever.

Adds `price_coverage`, mirroring `corporate_event_coverage`, and removes the
synthetic forward-filled rows that existed largely to make the inference come
out contiguous. The carry-forward moved into the valuation query, bounded by
coverage, so nothing in the price tables is derived.

Delivered across five PRs: the table and write path; gap analysis and the
fetcher recording negative answers; read-time carry-forward and the removal of
the `synthetic` column; the export emitting a global declaration plus
exceptions; and the spec and ADR.

See adr/0023-price-coverage-is-stored-not-inferred.md.
