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

## The record

An admitted association is never stored. That two identifiers denote one security
is recorded only by both rows carrying one `instrument_id` -- co-location, not an
edge. A recorded claim is the case that cannot express, because its two
identifiers sit on *different* instruments precisely because nothing joined them.
So this is the one place in the schema where an association is a row of its own.

One table, shared with the hypotheses 0143 raises, on a `kind` discriminator: a
claim awaiting a person and a claim awaiting an identifier plugin are the same
shape, and adr/0062 asks for one surface rather than two. It holds:

- Both endpoints as **whole triples** rather than instrument ids. Instruments are
  merged away and deleted; a triple survives both, so the claim outlives whatever
  it currently resolves to. The instrument ids are carried too, as a cache to be
  re-resolved rather than as the claim itself.
- The **mediator** triple, where a chain produced the claim, and null for a plain
  contradiction. Without it an admin sees a refusal with no reason attached.
- The **owner**, the ingestion source and the file's vintage. Owner is what lets
  0142's sweep count distinct users who reached the same claim; source is what
  makes a systematically wrong converter read as a cluster rather than as
  scattered rows; vintage is what separates a restatement from a contradiction.
- **State**, and what settled it -- an identifier plugin call, a promotion, or an
  admin. Three routes in, so the row has to say which was taken.
- **first_seen and last_seen.** Re-uploading a statement must not raise a
  duplicate: natural key on the endpoints, the mediator and the owner, and a
  repeat bumps `last_seen`.

## Confirmed and refuted are not symmetrical

A **confirmed** claim is deleted. The merge it authorises is the record, and
nothing re-derives the claim afterwards because the identifiers are now
co-located, so resolution finds one instrument without needing the chain. That a
merge rested on a broker's word and an identifier plugin's confirmation is a run-scoped fact
and belongs in telemetry (adr/0053), not in an operational table.

A **refuted** claim is kept permanently, and this is the part that does not fit
the "queue of unsettled work" reading. Nothing in the schema can say two
identifiers are *not* one instrument -- `instrument_identifiers` records only
co-location -- so a refutation has no other home. Delete it and the next upload of
the same statement re-derives the claim, re-raises it, and pays for the same
identifier plugin call again.

It follows that resolution reads this table. Not to decide what a value denotes,
which stays co-location's job, but to avoid asking a question already answered.
A refutation is a belief like any other, just a negative one.

**Superseded** is not terminal. It means an instrument moved underneath the claim
and it needs re-resolving, which puts it back in the queue.

Open: whether the queue and the refutations want one table or two. They have
different readers -- an admin surface and the sweep against resolution -- different
lifecycles and different growth. Two tables make the belief half honest about
being a belief; one buys a single natural key and a single surface.

See adr/0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md.
