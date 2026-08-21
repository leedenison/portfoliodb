# The server owns transaction grouping

[0021](0021-converters-own-transaction-grouping.md) put grouping in the
broker-specific converter, on the grounds that only the converter knows how a broker
reports the legs of an event, and that the broker's own reference numbers were
discarded before the data reached the standard format. The second half of that
premise no longer holds: the references are stored on the posting.

**The server decides which postings are legs of one event.** The converter's job is
to synthesise its broker's data into the standard evidence shape
([0048](0048-correlations-declare-their-own-semantics.md)); the server derives the
partition from that evidence over the whole of a user's data rather than over one
upload.

Three things argue for it. A converter sees one file, so any evidence that links
rows across uploads is invisible to it, and several ordinary paths leave the legs of
one event in separate groups: two legs arriving in two broker logs, and a group cut
by a period replace
([0040](0040-delete-window-widens-only-to-dataset-coverage.md)). The server
therefore has to be able to group whatever the converters do, and two
implementations with different reach is worse than one. And broker idiosyncrasy is
an argument about *translation*, not about *decision*: what a converter uniquely
knows is how its broker encodes the evidence, not what should be done with it once
encoded.

That last point needs one qualification, from
[0048](0048-correlations-declare-their-own-semantics.md). Not all converter grouping
is inference. For OFX it is transcription: the document nests a trade's legs and the
parser copies the containing `INVTRAN`'s `FITID` onto each one, so a server
re-deriving that by amount-and-date inference would replace a stated fact with a
guess. A source may therefore assert a grouping, and it does so as an ordinary
correlation the engine consumes as evidence -- not as a partition the engine obeys,
which is what this ADR and
[0043](0043-grouping-does-not-travel-in-the-archive.md) both reject.

## Considered: leaving the partition with the converter and giving the server only a merge

Cross-upload grouping is always a merge. Each upload's converter has already
partitioned its own rows, so what the server lacks is never the ability to split a
converter's group, only the ability to join two that separate converter runs could
not see together. A merge engine would therefore have satisfied every capability
argument above, and would have needed far less evidence -- correlation, amount,
type, date -- than a partition does.

It was rejected because it leaves two rule sets in place permanently. Grouping
quality would keep improving one converter at a time, in TypeScript, tested per
broker, with the server able only to repair what those rules produced. One engine
means one place to improve, one place to test, and every rule available to every
broker.

## Consequences

`group_ref` retires and grouping becomes derived state rather than an input, so the
archive carries postings and their correlations rather than a partition; see
[0043](0043-grouping-does-not-travel-in-the-archive.md). The store partitions the
postings it writes in the transaction that writes them, so an upload's legs are
joined to the ones already stored beside them rather than waiting for a cycle.

Group ids churn whenever the partition is recomputed. A machine-derived transfer
match is cache and the matcher rebuilds it
([0037](0037-transfer-matches-are-links-not-postings.md)). A human assertion needs
no anchor either, though that was not obvious here: it is a correlation written onto
its member postings, so it is a field of the thing it names and
[0002](0002-transaction-ingestion-model.md)'s missing natural key never comes up.
See [0049](0049-a-human-assertion-is-a-correlation.md).

Moving the decision does not make it broker-agnostic. The engine has to express what
the converters expressed: ordered passes that claim rows so a later pass cannot take
them, bucketing by account and date, amount equality within a tolerance, a
consideration cross-check, and directional distance between references. Expect
broker-specific passes on the server, not their disappearance -- and expect them
first as a per-broker precedence list over shared passes, since
[0047](0047-grouping-runs-as-precedence-ordered-passes.md) makes the pass order
load-bearing for correctness. So far one ordering serves both sources, and because
precedence is a table on the rules rather than the shape of the code, the day it
stops serving them is a table and not a restructuring.
