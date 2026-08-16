---
status: closed
title: A correlation can say a posting attaches to another posting's group
milestone: M15
dependencies: [0109]
---

Add `MATCH_ATTACHES`, the engine operation that consumes it, and the generic rule
that applies it, so a source reporting a subordinate leg as a record of its own can
say which record it belongs under.

## Motivation

A Fidelity dealing fee is its own row with its own reference number; a withheld tax
is its own row beside the dividend it was taken from. The leg is part of the event
rather than a peer of it, and `MATCH_EXACT` cannot say so: it is a peer relation, and
two postings sharing a token *make* a group.

Expressing the link as a shared token also breaks the trade rules. `Exact` runs at
precedence 1000, so stamping a trade's reference on its charge makes it claim
`{asset leg, charge}` first, and `trade.Apply` skips claimed postings -- the trade
never pairs with its cash leg and the cash row is stranded.

## Design

`MATCH_ATTACHES` is a directed reference to another posting's identifier, whose
bearer joins whatever group that posting is placed in. It is additive rather than
partitioning, so the rule consuming it runs below the rules that decide a partition.

`State.Attach(anchor, contributors...)` is the distinction `State.Claim`'s own
comment named as the one it cannot infer. The anchor is read and never written, so
irrevocability holds and no posting moves between groups.

`Attaches` at precedence 500 is one generic rule, not one per problem: it reads a
token, finds who carries it, and attaches. The judgement that produced the pointer
stays in the converter that made it.

See adr/0052-an-attaching-correlation-is-additive.md.

## Consequences

Nothing emits a pointer yet, so the exports goldens are unmoved. The Fidelity
converters start emitting them in 0111.

`Attaches.Expand` reaches through `PostingsByToken`, the index `Exact` already uses,
so no new access path is added.

`State.Group` is added so a rule can ask whether two postings are already together,
which is what tells three legs of one event from two postings that are not together.
