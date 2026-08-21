---
status: partly superseded by ADR-0053
---

# Telemetry counters and logging

The counters half is superseded by
[0053](0053-telemetry-is-run-scoped-event-rows.md); the logging half stands.

Telemetry counters were Redis integers under the `portfoliodb:counters:` prefix
with dot-separated suffixes (`<subsystem>.<subsystem>.<operation>.<outcome>`), so
the admin page could discover them by scanning the prefix rather than holding a
hard-coded list. Plugins never depended on Redis: the server injected a small
`Incr(name)` interface, which kept plugins unit-testable and unaware of the
backend. 0053 keeps that separation and changes its mechanism.

Logging uses the Go standard library `log/slog` only. slog covers levelled text
or JSON output to stdout, so a third-party logging library would add nothing.
