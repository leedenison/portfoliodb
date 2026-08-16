---
status: open
title: Telemetry schema and synchronous writer
milestone: M20
---

Add `server/migrations/005_telemetry.sql` creating the `telemetry` schema -- `run`,
`resolution_key`, `identification_attempt`, `identifier_plugin_call`,
`description_plugin_call`, one wide view per table, and SELECT-only grants for the
reading role -- and the writer behind it in `server/db`.

The writer uses its own connection pool and never joins the work's transaction, so a
rolled-back import still leaves the rows explaining why. Writes are synchronous; a write
that fails sets `telemetry_incomplete` on the run and is otherwise ignored.

Retention is a delete over `run.started_at` at 360 days, cascading to children.

Column lists and outcome vocabularies are in docs/spec/telemetry.md.
