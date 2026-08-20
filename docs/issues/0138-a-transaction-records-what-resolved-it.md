---
status: open
title: A transaction records the identifier that resolved it
milestone: M21
---

Record on each transaction which identifier resolution was given to find its
instrument, and make an identifier ownable by a user so a disagreement with a
shared mapping has somewhere to live.

## Motivation

Nothing records how a transaction came to point at the instrument it points at.
`txs` carries the description, the instrument id and no link between them; the
identifier hints the row arrived with are discarded with the job payload; the
source is reachable only through `tx_groups.job_id`, which deliberately has no
foreign key because a posting must outlive its job.

So a wrong association can be noticed but not traced. The question a correction
actually asks -- *which rows move if I fix this mapping* -- is unanswerable, and
a person who spots one bad holding has no way to find the others that came from
the same mistake.

Worse, the correction surface cannot tell what kind of thing it is looking at. A
row resolved from a broker's free text and a row resolved from an ISIN need
opposite treatment: the first is one user's reading of one source's wording and
is theirs to override, the second is shared reference data about a globally
unique code and a user overriding it is almost always hiding a data error rather
than fixing one. Without knowing which resolved the row, the UI cannot decide
whether to offer a person a button or escalate to an admin.

This becomes pressing with the candidate stage. A plugin that proposes an
identifier will sometimes propose one that is entirely valid and wrong, and
nothing downstream can detect that -- the round-trip check catches a guess that
lands in the wrong currency or asset class, not one that lands on a different
security in the same market. Whether the stage helps more than it harms is then
a question about correction cost, and correction starts with knowing what to
correct.

## Scope

**`txs.resolved_by`**: the `(identifier_type, domain, value)` triple of the key
resolution was given, null when nothing resolved the row.

A triple rather than a foreign key to `instrument_identifiers.id`, because
`mergeInstruments` deletes the loser's identifier rows and re-inserts them on the
survivor with new ids. Merges are eager and routine, so a foreign key would null
the provenance of every row whose deciding identifier belonged to a merged-away
instrument. The name survives a merge even when the row does not.

It is the key resolution was **given**, not what the instrument turns out to be
known by. An IBKR QFX states an ISIN; OpenFIGI is asked about the ISIN; the
ticker comes back in the answer. Recording that ticker would be recording the
conclusion as the premise. Where a source states two identifiers that both
resolve they must have agreed -- two that disagree fail the import with
`conflicting_hints` -- so recording the first one resolution consulted is
deterministic and not misleading.

**A user may own an identifier.** `instrument_identifiers` gains a nullable
`user_id`: null is the system mapping, of which exactly one may exist per name,
and it is the only one another user's transactions ever consult. Resolution reads
the user's own identifiers first and falls back to the system's. Never another
user's.

The exclusion constraint gains the owner, as `COALESCE(user_id, <sentinel>)`
rather than the bare column: a GIST `WITH =` on a null never conflicts, so a bare
column would permit unlimited system rows for one name, which is the opposite of
the rule. The same trap is already documented on `domain`.

This keeps the invariant rather than weakening it. "One name denotes one
instrument" becomes "one name denotes one instrument in a given user's view",
which is the version that was doing the work.

**An override does not overwrite the provenance.** A user's correction is an
identifier with the *same* triple as the system mapping it displaces -- same
name, different instrument -- so `resolved_by` is unchanged by it. Which owner's
row won is derivable: if the user holds an identifier with that triple, theirs
did. The disagreement is not recorded on the transaction at all; it is the
existence of a user-owned identifier whose instrument differs from the system one
with the same triple. That is a join, and it survives the transactions being
deleted and rewritten by the next upload, which a per-row marker would not.

It also gives admins a queue worth having: not "a user is unhappy" but "N users
have overridden this mapping", which is the evidence for deciding whether the
shared data is wrong.

**Not telemetry.** `telemetry.resolution_key` already records source,
description, hints and the instrument a key resolved to, and querying it would be
the cheap way to get this. It is the wrong source: telemetry lives in its own
schema on its own role with 360-day retention, and adr/0053's premise is that it
describes work rather than participating in it. A correction path that reads it
makes retention a correctness concern, and it is recorded at key grain rather
than row grain.

## Open

- Whether the correction surface refuses a user override on an identifier-resolved
  row outright, or accepts it as a request an admin adjudicates.
- Whether a user-owned identifier should be permitted at all for a globally unique
  code such as an ISIN, or only for the per-source vocabularies -- broker
  descriptions above all.
- What becomes of a user's override once the system mapping changes to agree with
  it. Leaving it is harmless but accumulates; removing it silently undoes a
  person's decision.
- Rows written before the column exists have no provenance, and nothing can
  reconstruct it. Whether that matters enough to re-resolve them.
- A row resolved from a broker description on Path B is resolved by a *proposed*
  key on its first upload and by the stored description on every later one. Both
  are true and they are different provenances; whether the first is worth keeping.
- An IBKR row has no `BROKER_DESCRIPTION` to correct at all, because Path A does
  not store one. Correcting such a row means correcting shared reference data
  until 0135 changes that.
