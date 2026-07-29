---
status: open
title: Harden the retroactive option-split guard against identified_at churn
---

Stop unrelated re-identification from disarming the retroactive option split
adjustment.

## Motivation

When a split lands on an option's underlying, the option's OCC symbol, strike and
contract terms need retroactive adjustment -- unless the option was identified
after we already knew about the split, in which case it is already correct. That
test compares `instruments.identified_at` against the split's knowledge time, and
it is the one genuine knowledge-vs-knowledge comparison in the system.

The guard is fragile in both directions:

- `identified_at` is set to `now()` by **every** `EnsureInstrument` call, not
  only by split-aware re-identification. Any unrelated import that touches the
  option after the split lands but before the adjustment runs makes the option
  look already-correct, permanently skipping an adjustment it needed.
- The adjustment pass only runs when splits landed in that same cycle, so a
  transient failure is never retried.

## Design

Separate "when we last ran identification" from "the knowledge state the stored
identity reflects" -- the latter is what the guard needs and it should only move
when the identity is actually re-derived against known splits. Make the
adjustment pass idempotent and retryable rather than tied to one cycle.

See docs/spec/bitemporality.md.
