---
status: open
title: Match or unmatch a transfer by hand
milestone: M12
dependencies: [0068]
---

Let an admin pair two sides of a transfer the matcher left alone, and break a pair it
got wrong.

## Motivation

0068 matches only on evidence that identifies the occurrence, and deliberately leaves
everything else alone: a cross-broker transfer, which no sample calibrates, and any
pair whose candidates it cannot tell apart. Those are exactly the cases a person
looking at the transfers report can resolve at a glance, and there is no way to record
the answer.

The other half is the wrong pair. A heuristic match is disposable by design, but
nothing can currently remove one, so an error stands until the groups are replaced by a
re-upload.

## Design

The storage already allows both. `transfer_matches.method` carries `MANUAL`, and the
matcher only ever inserts -- never updates, never deletes -- so a hand-made match
survives every rebuild. What is missing is the API and the UI.

- Create and delete a match from the transfers view of `/admin/imbalance`, where the
  unmatched sides are already listed.
- Deleting a heuristic match makes both sides unmatched again, and the next cycle will
  re-propose it. Something has to stop that: either a deletion is recorded so the pair
  is not re-proposed, or a deletion is only offered together with a replacement match.
  Settle which when this is picked up.

## Staleness

A hand-made match can stop making sense without anyone touching it: a later upload can
change the residual of a group it names, or remove the clearing leg it was made
against. 0095 carries the question of what happens then -- survive, drop, or flag for
review -- and it needs an answer here too, because this is where the match is created.
Related but separate is that a regroup churns group ids, so the anchor a manual match
is keyed on has to outlive the groups it names; that is settled in 0097.
