# Telemetry counters and logging

Telemetry counters are Redis integers under the `portfoliodb:counters:` prefix
with human-readable, dot-separated suffixes
(`<subsystem>.<subsystem>.<operation>.<outcome>`). The convention exists so the
admin page can **discover** counters by scanning the prefix and group them
hierarchically with no hard-coded list of counter names in the UI, and so the
naming stays consistent across plugins.

Plugins must not depend on Redis. The server injects a small counter interface
(e.g. `Incr(name)`) that plugins call to report metrics; the implementation
prepends the prefix and issues the Redis `INCR`. Keeping Redis out of plugin code
means plugins stay unit-testable and unaware of the telemetry backend.

Logging uses the Go standard library `log/slog` only, with no third-party logging
library — slog covers levelled text or JSON output to stdout, so a dependency
would add nothing. `LOG_LEVEL` currently defaults to `debug` (provisional) so the
OpenFIGI and identification paths are visible while those subsystems are being
stabilized; the default is expected to move to `info` later.
