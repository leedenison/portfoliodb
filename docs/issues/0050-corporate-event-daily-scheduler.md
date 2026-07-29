---
status: open
title: Daily corporate-event scheduler and blanket split recompute
---

Add the daily scheduler specified in docs/spec/corporate-events.md so newly
effective splits and freshly announced events are picked up without a manual
trigger.

## Motivation

`split_factor_at` bounds its product with `ex_date <= CURRENT_DATE`, so a split
stored via the 30-day lookahead sits inert until its ex_date passes. Something
has to notice the crossing and recompute. The spec says that is a daily call to
`RecomputeSplitAdjustments(ctx, "")`, but **no caller passes `""`** -- every
existing call site is per-instrument and triggered by new splits landing or a
transaction import. A stored future-dated split therefore never becomes
effective unless an unrelated event happens to re-touch the instrument.

This is the highest-priority divergence in docs/spec/bitemporality.md.

## Design

The required behaviour is already specified in docs/spec/corporate-events.md
under "Daily scheduler (planned)", including the two ordered steps, the
configuration, the skip conditions and the tests. This issue is to implement it.
