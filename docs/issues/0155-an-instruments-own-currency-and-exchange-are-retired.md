---
status: open
title: An instrument's own currency and exchange are retired
milestone: M25
dependencies: [0151, 0152, 0153, 0154]
---

The last place the old grain survives.

## Scope

Drop `instruments.currency`, `instruments.exchange_mic` and the `exchange`
denormalisation derived from it; `recompute_instrument_name` stops computing
`exchange` and keeps computing `name`, which is a security fact.
`Instrument.exchange_info` becomes per-listing, populated by joining
`listing_venues` to `exchanges`.

Mechanical, and expected to run past the PR size guidance for that reason.
Closes out what 0099 asked for: the foreign key to `exchanges`, the single-table
exchange filter in `ListInstruments` and the `exchange_info` join all survive on
`listing_venues`, and divergence between the column and the identifier domain is
no longer representable because there is no column.
