---
status: closed
title: Preserve knowledge time across corporate-event export, import and coverage merge
---

Stop corporate event round-trips and coverage merges from destroying knowledge
time.

## Motivation

`stock_splits.first_known_at` decides whether an option's OCC symbol still needs
retroactive adjustment, so losing it changes behaviour, not just auditability.
Two paths lose it:

- **Export and import.** `SplitRow` carries `ex_date`, `split_from` and
  `split_to` but no knowledge time, so a re-imported split is stamped with the
  import time. Retro-importing a historical split then makes every existing
  option look identified-before-we-knew, triggering adjustment of symbols that
  were already correct.
- **Coverage merge.** Merging adjacent or overlapping `corporate_event_coverage`
  spans deletes them and inserts one row stamped `now()`. A span covering
  2015-2026 then claims to have been confirmed today even though the 2015-2020
  portion was last queried years ago.

## Design

- Carry knowledge time on the corporate event wire types so a round-trip is
  lossless.
- Either keep coverage spans unmerged, or carry the oldest constituent
  `last_fetched_at` through a merge rather than resetting it. Merging is what
  makes gap subtraction cheap, so preserving the timestamp is likely the better
  trade.

See docs/spec/bitemporality.md.
