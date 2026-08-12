---
status: open
title: Derive transaction groups on the server
milestone: M15
dependencies: [0096, 0100, 0102]
---

Build the pass that decides which postings are legs of one economic event, from
stored evidence, over the whole of a user's data.

## Motivation

adr/0041-server-owns-transaction-grouping.md sets out why. In short: a converter sees
one file, so it cannot join legs that arrive in separate uploads, and it cannot repair
a group that a period replace cut
(adr/0040-delete-window-widens-only-to-dataset-coverage.md). The server has to be able
to group regardless, and one engine is better than two rule sets with different reach.

## Design

The engine has to express what `assignFidelityGroups` expresses today, so it is a rules
engine rather than a lookup:

- passes in a fixed precedence order, where an earlier pass claims a row so a later one
  cannot take it, and global ranking of candidates *within* a pass so one claim cannot
  strand another (adr/0047-grouping-runs-as-precedence-ordered-passes.md)
- bucketing by account and date for some passes and not others
- amount equality within a tolerance, and a consideration cross-check from independent
  fields
- directional distance between correlation ordinals, bounded by the span the source
  declares (adr/0048-correlations-declare-their-own-semantics.md)

A pass may claim a row only if the row's declared type set admits that pass's type, and
**the pass that claims a row is what resolves its type** -- there is no narrowing phase
after the partition. That is what dissolves the apparent circularity of grouping
consuming the type as evidence while the type depends on the group
(adr/0044-tx-type-is-declared-and-resolved.md).

The first pass is an exact correlation token match, which is how a source that states
its own grouping -- OFX nests a trade's legs under one `FITID` -- is honoured without
the server re-deriving it by inference.

A regroup deletes the group's routed residual postings and routes fresh ones in the
same transaction as the membership change. Stored weights do not move
(adr/0046-declared-ambiguity-is-bounded-by-weight-neutrality.md), but membership,
`resolved_tx_type` and the residuals do, and leaving the residuals to a later statement
exposes a moment where the deferred balance constraint fires on data that was valid
before the regroup began.

It runs post-ingest, on the signalling the transfer matcher already uses: an admin RPC
for a cadence and a trigger when an import commits (adr/0037-transfer-matches-are-links-not-postings.md).

**Blast radius.** A partition over the entire dataset per upload is not wanted. Group
the neighbourhood of the changed postings and widen until it is stable. Unlike an
upload window this can be widened freely, because it reads stored data and fetches
nothing.

**Validation.** The converters are the oracle. Their rules are pinned against the
sample exports with exact counts -- sells 88/88, buys 92/92, 21 deposit runs -- so
the engine can run in shadow over stored postings and be required to reproduce
`group_ref` exactly before anything depends on it. Do this before 0098 flips the
switch.

The oracle is not infallible, and the bar has to be read with that in mind. The
engine is free to disagree with a converter's pairing, including one that balances
with nothing worse than a `SOURCE_ROUNDING` residual, because that is what a
wrong pairing of two similar trades looks like
(adr/0047-grouping-runs-as-precedence-ordered-passes.md). So a divergence may be
the engine being right. Zero divergences is still the bar to start from, since
any other number is unfalsifiable, but each one is looked at rather than counted
as a failure.

## Settled: how a human assertion survives

adr/0049-a-human-assertion-is-a-correlation.md answers the question this issue was
asked to settle, and answers it by removing it. A hand-made grouping is a correlation
written onto the postings it names, with a synthesised token, `MATCH_EXACT` and a new
`SCOPE_USER`, so there is no anchor to resolve: the assertion is a field of the thing
it names. It dies with a re-upload, which the user is warned about rather than having
rebuilt, and survives an archive round trip because the archive carries correlations.
The engine consumes it through the same exact-token pass that honours an OFX `FITID`,
which is what "one design should serve both asserters" comes to.

A hand-made transfer *match* is a different object -- a link between groups, not a
join between postings -- and keeps its group ids. What makes that safe is the engine
writing only the groups it disagrees with, so a cycle that repartitions nothing churns
no ids. That is a requirement on this issue's write path, not a detail of it.

The writer, the `SCOPE_USER` value, the manual-grouping API and UI, and the re-upload
warning land in 0095.

## Evidence this needs and does not have

The converters compare two independently transcribed cash totals; the standard format
carries only one of them, because a security trade row's `Amount` is used inside
`assignFidelityGroups` and then discarded. 0102 closes that before the engine is
built.
