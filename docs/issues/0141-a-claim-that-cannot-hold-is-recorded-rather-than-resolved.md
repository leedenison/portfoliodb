---
status: open
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

The surface a recorded contradiction lands on is the one **M21** is scoped for
and **0127** repairs from, and the one 0142 needs for users who disagree.

See adr/0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md.
