---
status: open
title: Price fetch cycles emit run-scoped telemetry
milestone: M20
dependencies: [0116, 0117]
---

A price fetch cycle opens a run and stamps it, and records nothing else. It is the
longest running of the cycles and the only subsystem with no child telemetry at all,
so its run duration is the whole of what can be said about it: when a cycle takes
twenty minutes instead of two, nothing says which plugin, which instrument, or how
much was asked for.

Record the two grains the orchestrator already works in -- one instrument's outstanding
gap, and one range put to one plugin -- so that a slow cycle can be attributed, an
instrument that will never price can be found, and the coverage recording that stops a
range being asked about forever can be seen working.
