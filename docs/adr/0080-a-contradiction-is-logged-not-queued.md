# A contradiction is logged, not queued

[0064](0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md) decided that a
claim which cannot hold gets a durable record for a person rather than being
settled by a guess. It did not say where that record lives, and the obvious
reading was an operational table: a queue of unsettled claims with a state
machine, permanent refutations, and an admin surface to work them from.

That is the wrong shape, for a reason that is about the product rather than
about the schema. These contradictions are rare, and the system is meant to
decide what to do about them on its own -- degrade the instrument, exclude the
lower-precedence result, leave two instruments apart. A queue implies somebody
works it, and building one commits us to a conflict-resolution UI that the
handling rules were specifically designed to avoid needing.

What is actually missing is not a queue but the answer to "what happened, and
how often". That is what [0053](0053-telemetry-is-run-scoped-event-rows.md)
exists for.

## The decision

Contradictions and merge decisions are run-scoped telemetry rows. They are
counted, charted and drilled into; they are not worked.

The run framing needs no extension to carry them. `telemetry.run` already covers
the corporate event cycle and the price fetch cycle alongside the three import
kinds, so the merges taken outside instrument resolution have a parent. Four of
the five sites already wrote a row with a closed-vocabulary outcome --
`discarded_inconsistent`, `conflicting_hints`, `not_attempted_conflicting_hints`
-- so what was missing was the evidence behind the count rather than the count.

Grafana and the admin UI both read the telemetry schema. 0053 ended "Nothing in
the SPA reports telemetry: it is read in Grafana", which was a statement about
the Redis counters having no reader worth building rather than a boundary worth
defending. What needs a person's attention is a legitimate question to ask of
recorded events.

**No functional path may depend on data in telemetry.** Telemetry records what
happened; every decision the system takes is made from operational data. This is
what makes the retention window safe: a row that can be purged cannot be load
bearing, and a table nothing reads back cannot drift into being an operational
one. It is also what settles the shape of anything moved into the schema -- a
mutable flag an operator sets is state, not an event, and does not come.

## What it costs

A refutation is not durable. The queue design kept "these two identifiers are
*not* one security" permanently, so a pair an identifier plugin had already
refuted was not asked about again. Nothing else in the schema can say that --
`instrument_identifiers` records only co-location -- so dropping the table drops
the fact, and every upload of the same statement re-derives the claim and pays
for the same plugin call. That is a quota cost rather than a correctness one:
the refusal itself is re-derived correctly each time.

Reading a refutation back out of telemetry to avoid the call is exactly what the
rule above forbids, so this is not a gap to be closed later by pointing
resolution at the schema. If the cost turns out to matter, the answer is an
operational fact about the association, decided on its own terms.

## Consequences

- [0064](0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md) is amended:
  its three-way split -- reject a self-contradicting file, degrade and accept a
  file that disagrees with the database, reject a transaction fault -- stands
  unchanged. Only where the record lands changes.
- Issue 0141's "The record" section, which specified the operational table, is
  superseded by this.
- 0142's promotion route and 0143's hypothesis both parked on the surface 0141 was
  going to build. The promotion needed nothing from it. The hypothesis has nowhere left
  to wait, so a claim that would move a fact is refused, recorded here and resolved to
  one instrument (issue
  [0143](../issues/0143-a-claim-that-would-move-a-fact-is-refused.md)).
- `unhandled_corporate_events` is the one existing table that is telemetry
  wearing an operational shape, and it moves. Its `resolved` flag goes with it,
  under the rule above.
