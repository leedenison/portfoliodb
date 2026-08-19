---
status: open
title: Per-field candidate telemetry
milestone: M17
dependencies: [0131]
---

Record each field a candidate plugin proposed and what became of it, so "did
completion help" is a query rather than an opinion.

## Motivation

`description_plugin_call` counts items and tokens per call. A call spans many
resolution keys, so nothing joins a proposed value to the instrument that was
eventually resolved, and the question 0105 asks -- how often was the added field
confirmed, and at what cost -- cannot be answered from the rows that exist.
Counters cannot answer it either, at any granularity: the join is the point.

## Scope

A child table holding one row per proposed field per resolution key, naming both
parents -- the call it came from and the key it was proposed for -- with the
field, the value, the confidence and an outcome. Confirmed when the winning
instrument agrees, contradicted when it differs, untested when the instrument
says nothing about that field, unused when the key resolved without reaching
plugins. The same `CompareHints` pass that produces hint diffs computes it.

`resolution_key.extraction_outcome` becomes a candidate outcome with a
vocabulary that fits the new gate: fields proposed, nothing proposed, and the
reasons the stage was not attempted -- a DB hit, an already-complete identity, a
type filter, no plugins. `not_attempted_hints_supplied` retires; supplied hints
stop being a reason once 0131 lands.

A view joining call, key and run, so accuracy by field and cost per resolution
are each one query, and confidence can be bucketed against outcome to see
whether it means anything.
