---
status: open
title: Derive transaction groups on the server
milestone: M15
dependencies: [0096, 0100]
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
sample exports with exact counts -- sells 91/91, buys 78/78, deposit runs 21/21 -- so
the engine can run in shadow over stored postings and be required to reproduce
`group_ref` exactly before anything depends on it. Do this before 0098 flips the
switch.

## Open

A regroup churns group ids, so a human assertion -- a hand-made transfer match, and
later a hand-made grouping -- cannot be keyed on them. Human judgement has to become an
input replayed on every run, anchored on something durable, and a posting has no
natural key (adr/0002-transaction-ingestion-model.md). Settle this here rather than in 0091 or 0095, both of which
depend on the answer.

A source-asserted grouping is the same mechanism with a different asserter: an
assertion, replayed on every run, that the engine consumes as its
highest-precedence evidence (adr/0048-correlations-declare-their-own-semantics.md).
One design should serve both.
