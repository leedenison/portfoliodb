# A claim that cannot hold is flagged, not resolved

Amended by [0080](0080-a-contradiction-is-logged-not-queued.md), which puts the
record in the telemetry schema rather than in an operational queue. The split
below is unchanged; only where the record lands is.

Two authorities can contradict each other. Two identifier plugins return
different ISINs for one security; a broker file names a mapping the database
already holds differently; a merge finds the two instruments both wearing one
name over one interval. None of these can be settled from the data that produced
them, and each is currently settled anyway -- by a log line, by an error that
kills the import, or by dropping the colliding name and deleting the instrument
that held it.

The last of those is the worst, because it destroys the evidence. A merge that
cannot carry a name across does `continue` and deletes the loser regardless, so
afterwards nothing records that a contradiction was ever seen.

Detection is first come first served and that is irreducible: whichever claim
arrived first is in the database, and the second one is the one that looks wrong.
Nothing in the data says which is correct. So the response is a durable record
for a person, never a guess -- and specifically not a rejection of whoever
happened to be second. `CONID-X is ISIN-2` may be perfectly true and the stored
`CONID-X is ISIN-1` may be the error, inherited from a merge or from a plugin
that mis-associated it.

## Where the contradiction is decides the response

**Within one file.** Claims presented at one vintage that cannot all hold. No
reading of the validity intervals saves this, because they share a vintage -- the
artefact is faulty. The converter refuses it before upload and ingestion checks
again rather than trusting the converter. The upload is rejected.

**A file against the database.** An identity failure, and nothing more. The
transactions were never in doubt; only the question of which instrument they
belong to is. So the instrument degrades to broker-description-only -- the same
degradation a plugin timeout already produces, which is visible, repairable and
does not propagate -- the contradiction is recorded, and the upload is accepted.
Rejecting it would strand the user behind an admin, for a corporate action
neither of them knew about.

**A transaction fault.** An unparseable amount, an unbalanced group. The whole
upload is rejected. Accepting the sound rows and dropping the rest leaves
holdings in a state nobody can characterise, where rejecting the upload leaves
them describable as valid up to the date of the last accepted import. That
invariant is worth more than the rows.

The split is the point: an identity failure is not a transaction failure, and
treating them alike is what makes one unknown corporate action able to block a
statement.

## Consequences

- This amends [0004](0004-instrument-resolution-and-merge.md), which treats
  merge-on-conflict as always available and says nothing about a merge that
  cannot complete. Its "Identity is current state" section was already superseded
  by [0055](0055-identifier-validity-is-an-interval.md).
- A record has to outlive the call that made it. A plugin disagreement discovered
  during one upload is a durable fact about the security, and logging it means
  rediscovering and re-logging it on the next upload while nothing accumulates.
- The surface it lands on is the one
  [0063](0063-identity-claims-are-owned-until-users-corroborate-them.md) needs
  for users who disagree, and the one
  [0062](0062-a-user-mediated-claim-is-a-lead-not-a-write.md) needs to park a
  hypothesis on. Three problems, one shape: authorities that cannot both be
  right, awaiting a person.
