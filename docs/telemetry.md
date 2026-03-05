# Telemetry

## 1. Counters

### Overview

- **Storage**: Redis. Use a dedicated key prefix so counters can be discovered and separated from session keys (`portfoliodb:session:`). See **Key naming** below.
- **Semantics**: Each counter is a Redis key; value is an integer. Increment with `INCR`. No TTL; counters persist until explicitly reset or overwritten.
- **Discovery**: The admin page must **discover** which counters exist (e.g. `SCAN` or `KEYS` on the counter prefix) and show name + value. No hard-coded list of counter names in the UI.

### Key naming

- **Prefix**: `portfoliodb:counters:`
- **Suffixes**: Human-readable, dot-separated names (e.g. `instrument.identify.attempts`, `openfigi.mapping.succeeded`). Full key = prefix + suffix.
- This keeps discovery simple and the admin list readable.

### Admin page

- **Location**: New admin route, e.g. `/admin/telemetry` or `/admin/counters`, linked from the admin sidebar.
- **Behaviour**: Renders a dynamically expandable list of counter names and current values. Counter names are the key suffix after the prefix (e.g. `instrument.identify.attempts`). Values are read from Redis (e.g. `GET` per key, or `MGET` for all keys under the prefix). Optional: refresh button or short auto-refresh.
- **Auth**: Admin-only (same as other admin pages).

### Initial counters

1. **Instrument identification attempts**  
   - **Key**: `portfoliodb:counters:instrument.identify.attempts`  
   - **Increment when**: A resolve reaches the “call enabled plugins” path (DB and batch cache miss, and there is at least one enabled plugin). One increment per such `Resolve(...)` call (i.e. per distinct (source, instrument_description) that we try to identify via plugins).  
   - **Placement**: In `server/service/ingestion/resolve.go`, once we have `inputs` and `len(inputs) > 0`, before starting the plugin goroutines.

2. **OpenFIGI outcomes**  
   Increment in the OpenFIGI plugin/client for each mapping or search call:

   - **Mapping**  
     - `portfoliodb:counters:openfigi.mapping.succeeded` — mapping returned at least one result.  
     - `portfoliodb:counters:openfigi.mapping.zero_results` — mapping returned no results (empty data, no API error).  
     - `portfoliodb:counters:openfigi.mapping.rate_limit` — HTTP 429.  
     - `portfoliodb:counters:openfigi.mapping.failed` — any other error (non-200, API error message, etc.).

   - **Search**  
     - `portfoliodb:counters:openfigi.search.succeeded` — search returned at least one result.  
     - `portfoliodb:counters:openfigi.search.zero_results` — search returned no results.  
     - `portfoliodb:counters:openfigi.search.rate_limit` — HTTP 429.  
     - `portfoliodb:counters:openfigi.search.failed` — any other error.

   **Placement**: In `server/plugins/openfigi/identifier/openfigi.go`, after each `Mapping` and `Search` call, increment the appropriate counter. The plugin receives a **counter interface** (injected by the server); it does not depend on Redis directly.

### Counter interface (injected into plugins)

- Plugins must **not** depend on Redis. The server injects a small counter interface so that plugins can report metrics without importing Redis or the telemetry implementation.
- **Interface**: A single method, e.g. `Incr(name string)` or `Incr(ctx, name string)`, where `name` is the counter suffix (e.g. `openfigi.mapping.succeeded`). The implementation (in the server or a shared telemetry package) prepends `portfoliodb:counters:` and calls Redis `INCR`.
- **Wiring**: When the server constructs or invokes plugins (e.g. identifier registry, ingestion worker), it passes an implementation of this interface. The ingestion worker also receives an implementation (backed by the same Redis client) for `instrument.identify.attempts`. The OpenFIGI plugin receives the interface and calls it from `openfigi.go` after each Mapping/Search.

### API for the admin page

- New gRPC (or HTTP) admin-only method that returns a list of `{ name, value }` by scanning Redis for `portfoliodb:counters:*`. Counter names shown in the UI are the key suffixes (the part after the prefix).

---

## 2. Logger

### Overview

- **Output**: Standard out (stdout).
- **Level**: Controlled by an environment variable (e.g. `LOG_LEVEL`). For now use **debug** as the default so that OpenFIGI and identification paths are visible during debugging.
- **Behaviour**: When OpenFIGI is invoked (mapping or search), log at debug (or info) that we’re calling the API (and optionally the input, e.g. ticker or query). On success, log success; on error, log the error (message and optionally status code / response body summary).

### LOG_LEVEL

- **Env var**: `LOG_LEVEL` (or `PORTFOLIODB_LOG_LEVEL` if we want to namespace).
- **Values**: At least `debug`, `info`, `warn`, `error`. Default: `debug`.
- **Implementation**: Use a standard Go logger that supports levels (e.g. `log/slog` from the standard library). Only emit a log line if its level is >= the configured level.

### OpenFIGI log points

- **On invoke**: When calling OpenFIGI mapping or search, log once per call with:
  - Operation: mapping or search.
  - Input: e.g. job ID/value for mapping, query (and optional exchCode) for search.
  - Level: debug (or info).
- **On success**: Log that the call succeeded and optionally result count (e.g. “mapping returned 1 result”). Level: debug.
- **On error**: Log that the call failed, with:
  - Error message (and for HTTP errors: status code, and optionally a short summary of the response body).
  - Level: error (or warn for rate-limit if we want to distinguish).

### Placement

- **Logger**: Initialized at server startup from `LOG_LEVEL`, stored in a package or passed where needed (e.g. to the OpenFIGI client or plugin).
- **Call sites**: In `server/plugins/openfigi/identifier/openfigi.go` (and optionally in `plugin.go` for “Identify started” / “Identify succeeded” / “Identify failed” if we want a single place for “OpenFIGI invoked”). Prefer one place (e.g. `Mapping` and `Search` in `openfigi.go`) so all HTTP-level outcomes are logged there.

### Dependencies

- Prefer **stdlib only** for T15: `log/slog` (Go 1.21+) is sufficient. No need for a third-party logging library unless we want structured JSON output to stdout; slog can do JSON or text.
