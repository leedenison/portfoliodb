---
status: closed
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

## Resolution

Fixed, and the comparison turned out to be on the wrong clock as well.

`identified_at` is now `instruments.identity_as_of` -- the point in market time
the stored identity reflects -- and the guard compares it against the split's
`ex_date` rather than its `first_known_at`. OCC adjusts a contract on the
effective date, and providers list the pre-split symbol until then, so an
identity derived before the ex_date does not reflect the split however long we
had known it was coming. The old comparison marked such an identity
already-correct and skipped the adjustment permanently. It also put the guard on
the same clock as `AdjustOCCForKnownSplits`, which had always filtered purely on
`ex_date`. The reasoning is in adr/0017-option-identity-reflects-ex-date.md.

The column now moves only when the identity is genuinely re-derived: a plugin
identification, or the adjustment itself. `EnsureInstrument` never writes it, on
create or on match, because it cannot know the provenance of the identifiers it
was handed; each caller stamps what it knows, and the column only ever moves
forward so a stale file cannot drag it backwards.

The pass is now query-driven. `ListPendingOptionSplits` returns every option
whose identity predates an effective split on its underlying, so the work list is
a function of stored state rather than of which splits happened to arrive in the
current cycle. It runs once per cycle, unconditionally: a failed adjustment
leaves the option pending and the next cycle retries it, where previously
coverage had already been written and the pass was never reached again. A
future-dated split is simply not pending until its ex_date passes.

Each option is adjusted once by the cumulative factor of all its pending splits.
The previous per-split loop read the option row once and never refreshed it, so
two splits divided the original strike twice and left the option carrying an OCC
identifier per split -- reachable whenever a backfill landed two historical
splits together. A split that cannot be applied blocks its option rather than
being skipped over, and leaves it pending for a later run.
