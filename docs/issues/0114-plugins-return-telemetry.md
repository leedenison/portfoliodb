---
status: open
title: Plugins return telemetry rather than incrementing counters
milestone: M20
---

Change `identifier.Plugin.Identify` and `description.Plugin.ExtractBatch` to return a
telemetry value alongside their result, and drop the injected counter from every plugin.

A plugin-call row needs facts from two parties: the plugin knows its transport outcome,
its retries and its token usage, while only the orchestrator knows whether that plugin
won, was superseded by a better hint match, or was discarded as inconsistent with the
winner -- decided after every plugin has returned. Having the plugin write its own row
instead would make plugins depend on the telemetry backend and drag a database into
every plugin test, which is what the injected counter existed to avoid.

Plugins consequently stop importing `server/telemetry` altogether. Touches EODHD
(identifier, price, corporate events), Massive (identifier, price), OpenAI, OpenFIGI and
the inflation plugin.

See adr/0053-telemetry-is-run-scoped-event-rows.md and docs/spec/telemetry.md.
