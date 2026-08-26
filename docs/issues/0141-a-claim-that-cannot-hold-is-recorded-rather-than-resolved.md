---
status: closed
title: A claim that cannot hold is recorded rather than resolved
milestone: M24
dependencies: [0139]
---

Every site that meets a contradiction settles it, and none of them can.

`mergeInstruments` is the worst: an identifier the survivor already holds over
an overlapping interval hits `isIdentifierConflict` and is skipped with
`continue`, and the losing instrument is deleted regardless. The collision is
the evidence that two claims cannot both hold, and dropping it is the one
outcome that leaves nothing to look at afterwards. `consistentWith` discards a
result whose identifier value disagrees with the winner's and says so in a log
line, which is not queryable, is not attached to the security, and is
rediscovered and re-logged on every upload without accumulating. Resolution
raises conflicting stated hints as an error that propagates out of the row and
kills the batch. The price worker reports ambiguity as an opaque error. No
converter and no ingestion check notices a file that contradicts itself.

Detection is first come first served and that cannot be fixed: whichever claim
arrived first is stored, and nothing in the data says it is the right one. So
the response is a durable record for an admin, and specifically not a rejection
of whoever was second.

Which response applies depends on where the contradiction is. Claims at one
vintage inside one file cannot be reconciled by any reading of the intervals, so
the artefact is faulty and the upload is rejected -- the converter refuses it and
ingestion checks again rather than trusting the converter. A file disagreeing
with the database is an identity failure and not a transaction failure: the
instrument degrades to broker-description-only exactly as an identifier plugin
timeout already causes, the contradiction is recorded, and the upload is
accepted, because blocking it would strand the user behind an admin over a
corporate action neither knew about. A transaction fault rejects the whole
upload, so holdings stay describable as valid up to the last accepted import.

A recorded contradiction is read rather than worked: it is a telemetry row, and
both Grafana and the admin UI may read the telemetry schema. It is not the
attention surface **M21** builds and **0127** repairs from -- what needs a person
is a separate question from what happened.

## The record

A run-scoped telemetry row, not an operational queue. These contradictions are
rare and the system settles each of them on its own; what is missing is not a
queue somebody works but the answer to what happened and how often. A queue
commits us to the conflict-resolution UI the handling rules were designed to
avoid needing. See adr/0080-a-contradiction-is-logged-not-queued.md.

The run framing carries it unchanged. `telemetry.run` already covers the
corporate event cycle and the price fetch cycle beside the three import kinds, so
the merges taken outside instrument resolution have a parent, and four of the five
sites already write a row with a closed-vocabulary outcome. What is missing is the
evidence behind the count rather than the count.

Two grains:

- **`telemetry.merge`**, one decision about whether two identifiers denote one
  security: both endpoints as whole triples, the instruments they resolved to, and
  an outcome of `merged` or one of four refusals. Nothing recorded a merge at all
  before -- the decision is taken inside the database layer, where there was no run
  to hang a row off, so a merge, a refusal and a name silently dropped looked
  identical from outside.
- **`telemetry.identity_conflict`**, the two triples behind a
  `discarded_inconsistent`, a `conflicting_hints` or an ambiguous identifier.

A file that contradicts itself at one vintage records nothing: the artefact is
faulty and the upload is rejected, which is a validation error rather than a
diagnostic.

Nothing functional reads either table. That is what makes the retention window
safe, and it is what this gives up: a pair an identifier plugin has refuted is
asked about again on the next upload of the same statement, because a purged row
cannot stop it and reading telemetry to make an operational decision would invert
the dependency. Quota rather than correctness.

The three routes out of a claim -- an identifier plugin, a promotion, an admin --
were properties of the queue and go with it. **0142**'s promotion and **0143**'s
hypothesis both parked on the surface this was going to build, and both need
re-scoping.
