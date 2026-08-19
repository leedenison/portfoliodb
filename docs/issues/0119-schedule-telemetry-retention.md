---
status: open
title: Schedule the telemetry retention purge
milestone: M23
dependencies: [0115]
---

Call the `PurgeTelemetry` RPC on a schedule, so runs past the 360-day window are
actually deleted rather than merely deletable.

The RPC exists and does the work; nothing invokes it. The service schedules nothing
of its own, so this is an operator-side scheduler -- a cron entry, a compose sidecar
or whatever the deployment already uses -- authenticating as an admin service account
and logging the count the RPC returns.
