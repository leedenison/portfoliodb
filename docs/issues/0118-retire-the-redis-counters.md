---
status: closed
title: Retire the Redis counters and the admin telemetry page
milestone: M20
dependencies: [0116, 0117]
---

Remove every defined counter, the `/admin/telemetry` page and the
`ListTelemetryCounters` RPC behind it. The counter infrastructure in `server/telemetry`
stays, with no callers, so a counter can be reintroduced without rebuilding it.

Nothing is deleted until the dashboards replacing it are in place, which is what the
dependencies say.
