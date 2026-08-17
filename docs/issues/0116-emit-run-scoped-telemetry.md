---
status: closed
title: Emit run-scoped telemetry from ingestion, identification and the workers
milestone: M20
dependencies: [0114, 0115]
---

Open a run for every ingestion job and every worker cycle, and emit the event rows
beneath it: one resolution key per distinct (source, description, hints) triple, one
identification attempt per `ResolveWithPlugins` call, one row per plugin invocation.

A resolution key is not a transaction -- many transactions share one and resolve once --
so `tx_count` carries the fan-out. `purpose` distinguishes the primary attempt from the
two the MIC_TICKER against OPENFIGI_SHARE_CLASS mismatch check makes and from underlying
recursion; those two are currently made with a nil counter and are invisible.

Includes the startup sweep that stamps `incomplete` on runs left without a terminal
outcome, which is what lets a null outcome mean genuinely in flight. The worker registry
is unchanged: it keeps live gauges, the run table owns history.
