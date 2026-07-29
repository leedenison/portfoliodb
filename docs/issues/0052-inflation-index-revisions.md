---
status: closed
title: Preserve prior vintages of revised inflation index values
---

Keep the superseded value when a statistical agency revises an inflation index.

## Motivation

CPI and CPIH values are routinely revised after first publication.
`inflation_indices` is keyed `(currency, month)` and the upsert overwrites
`index_value` in place, so the prior vintage is unrecoverable. Any real-return
figure published before a revision cannot be reproduced afterwards, and there is
no way to tell a revision from a first observation.

This is the clearest revision case in the system: the valid time (`month`) is
fixed while the value legitimately changes, which is exactly the shape a
knowledge-time dimension exists to record.

## Resolution

Closed without building the vintage store. The system uses the latest published
figure and does not record that an earlier one existed.

Nothing consumes the stored values beyond gap detection and the admin listing --
there is no real-return metric, so the figure whose reproducibility motivated
this cannot yet be produced. ONS, the only implemented provider, does not revise
CPI or CPIH once published; it rebases periodically, and a real return is a ratio
of two index values, which a rebasing leaves unchanged. The fetch worker also
only requests months it has no data for, so a revision to a covered month would
never reach the upsert without a re-fetch window that no consumer justifies.

The reasoning is recorded in adr/0016-bitemporal-time-model.md and the resulting
behaviour in spec/bitemporality.md.
