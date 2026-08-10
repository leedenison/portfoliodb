# The server owns transaction grouping

[0021](0021-converters-own-transaction-grouping.md) put grouping in the broker-specific
converter, on the grounds that only the converter knows how a broker reports the legs
of an event, and that the broker's own reference numbers were discarded before the data
reached the standard format. The second half of that premise no longer holds -- 0021
records as much: the references are stored on the posting.

**The server decides which postings are legs of one event.** The converter's job is to
synthesise its broker's data into the standard evidence shape
([0048](0048-correlations-declare-their-own-semantics.md)); the server derives the
partition from that evidence over the whole of a user's data rather than over one
upload.

Three things argue for it. A converter sees one file, so any evidence that links rows
across uploads is invisible to it, and several ordinary paths leave the legs of one
event in separate groups: two legs arriving in two broker logs, and a group cut by a
period replace ([0040](0040-delete-window-widens-only-to-dataset-coverage.md)). The
server therefore has to be able to group whatever the converters do, and two
implementations with different reach is worse than one. And broker idiosyncrasy is an
argument about *translation*, not about *decision*: what a converter uniquely knows is
how its broker encodes the evidence, not what should be done with it once encoded.

## Considered: leaving the partition with the converter and giving the server only a merge

Cross-upload grouping is always a merge. Each upload's converter has already
partitioned its own rows, so what the server lacks is never the ability to split a
converter's group, only the ability to join two that separate converter runs could not
see together. A merge engine would therefore have satisfied every capability argument
above, and would have needed far less evidence -- correlation, amount, type, date --
than a partition does.

It was rejected because it leaves two rule sets in place permanently. Grouping quality
would keep improving one converter at a time, in TypeScript, tested per broker, with
the server able only to repair what those rules produced. One engine means one place to
improve, one place to test, and every rule available to every broker.

## Consequences

`group_ref` retires, and grouping becomes derived state rather than an input. The
archive carried transactions "and their grouping" as irreplaceable data;
[0043](0043-grouping-does-not-travel-in-the-archive.md) re-examines that against
[0032](0032-archive-preserves-inputs-not-derived-state.md) and
[0035](0035-archive-nests-by-aggregate-root.md) and drops the grouping from it.

Group ids churn whenever the partition is recomputed. A machine-derived transfer match
is cache and the matcher rebuilds it ([0037](0037-transfer-matches-are-links-not-postings.md)),
but a match or a grouping a person asserted by hand cannot be keyed on something that
evaporates. Human assertions have to become inputs to the grouping pass, replayed on
every run, which needs the postings they name to be identifiable -- an open design
question, and the one place [0002](0002-transaction-ingestion-model.md)'s "no natural
key" bites.

Moving the decision does not make it broker-agnostic. The engine has to express what
the converters express today: ordered passes that claim rows so a later pass cannot
take them, bucketing by account and date, amount equality within a tolerance, a
consideration cross-check, and directional distance between references. Expect
broker-specific passes on the server, not their disappearance.

## Amendments

**Not all converter grouping is inference.**
[0048](0048-correlations-declare-their-own-semantics.md) corrects the reading of
"broker idiosyncrasy is an argument about *translation*, not about *decision*"
above. For OFX the grouping is transcribed rather than inferred: the document
nests a trade's legs and the parser copies the containing `INVTRAN`'s `FITID` onto
each one. A server that re-derived that by amount-and-date inference would be
replacing a stated fact with a guess. A source may therefore assert a grouping,
and it does so as an ordinary correlation the engine consumes as evidence -- not
as a partition the engine obeys, which is what this ADR and
[0043](0043-grouping-does-not-travel-in-the-archive.md) both reject.

**The broker-specific passes arrive as an ordering.**
[0047](0047-grouping-runs-as-precedence-ordered-passes.md) makes the engine's pass
order load-bearing for correctness rather than for tuning, and a single order has
no one broker's data to justify it against. So "expect broker-specific passes on
the server, not their disappearance" is likely to be realised first as a per-broker
precedence list over shared passes.

