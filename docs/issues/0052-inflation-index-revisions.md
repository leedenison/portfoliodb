---
status: open
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

## Design

Widen the key to include a knowledge time, or move superseded values to a
history table, so that the current value is a cheap lookup and the prior ones
remain readable. Likely a prerequisite for 0054.

See docs/spec/bitemporality.md.
