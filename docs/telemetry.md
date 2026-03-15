# Telemetry

## 1. Counters

### Overview

- **Storage**: Redis. Use a dedicated key prefix so counters can be discovered and separated from session keys (`portfoliodb:session:`). See **Key naming** below.
- **Semantics**: Each counter is a Redis key; value is an integer. Increment with `INCR`. No TTL; counters persist until explicitly reset or overwritten.
- **Discovery**: The admin page must **discover** which counters exist (e.g. `SCAN` or `KEYS` on the counter prefix) and show name + value. No hard-coded list of counter names in the UI.

### Key naming

- **Prefix**: `portfoliodb:counters:`
- **Suffixes**: Human-readable, dot-separated names following the convention `<subsystem>.<subsystem>.<subsystem>.<operation>.<outcome>`. Up to 3 subsystems segments, an optional operation and an outcome.  Full key = prefix + suffix.
  - **Segment 1 - (n - 2) (subsystem)**: The plugin or server subsystem (e.g. `instruments.resolution.totals`, `instruments.description.openai`, `instruments.identification.openfigi`, `instruments.identification.massive`).
  - **Segment (n - 1) (operation)**: The specific operation or feature within that subsystem (e.g. `ticker_mapping`, `ticker_search`, `ticker_extraction`, `ticker_metadata`).
  - **Segment (n) (outcome)**: The specific metric or outcome (e.g. `succeeded`, `failed`, `rate_limit`, `prompt_tokens`).
- This keeps discovery simple, supports grouping in the admin UI, and ensures consistency across plugins.

### Admin page

- **Location**: `/admin/telemetry`, linked from the admin sidebar.
- **Behaviour**: Counters are grouped by their dot-separated name segments into a hierarchical layout:
  - **Section** (segment 1): Full-width heading for each subsystem. Sections are rendered top-to-bottom.
  - **Card** (segment 2): Within each section, cards are arranged in a responsive two-column grid.
  - **Card Heading 1** (segment 3): Within each card are a series of ruled headings separating subsections.
  - **Card Heading 2** (segment 4): Within each main card heading are a series of indented sub-headings separating subsections.
  - **Entry** (segment 5): At whatever level we terminate (section, card, card heading 1, card heading 2), leaf counter values are listed with the outcome label and right-aligned numeric value.
- Counter names and grouping are derived dynamically from the keys discovered in Redis; no hard-coded list of counter names or sections in the UI.
- **Auth**: Admin-only (same as other admin pages).

### Counters

1. **Resolution counters** (server/service/ingestion/resolve.go)

   - `instruments.resolution.totals.describe.extraction_failed` -- description extraction failed; using broker-description-only.
   - `instruments.resolution.totals.describe.plugin_error` -- a description plugin returned an error.
   - `instruments.resolution.totals.describe.no_hints` -- plugins were tried but none returned hints.
   - `instruments.resolution.totals.describe.identifier_mismatch` -- TICKER and OPENFIGI_SHARE_CLASS hints resolved to different instruments.
   - `instruments.resolution.totals.identify.attempts` -- identifier plugins were invoked for a (source, instrument_description) pair.

2. **OpenFIGI outcomes** (server/plugins/openfigi/identifier/openfigi.go)

   - **Mapping**
     - `instruments.identification.openfigi.mapping.succeeded` -- mapping returned at least one result.
     - `instruments.identification.openfigi.mapping.zero_results` -- mapping returned no results (empty data, no API error).
     - `instruments.identification.openfigi.mapping.rate_limit` -- HTTP 429.
     - `instruments.identification.openfigi.mapping.failed` -- any other error (non-200, API error message, etc.).

   - **Search**
     - `instruments.identification.openfigi.search.succeeded` -- search returned at least one result.
     - `instruments.identification.openfigi.search.zero_results` -- search returned no results.
     - `instruments.identification.openfigi.search.rate_limit` -- HTTP 429.
     - `instruments.identification.openfigi.search.failed` -- any other error.

   **Placement**: In `server/plugins/openfigi/identifier/openfigi.go`, after each `Mapping` and `Search` call, increment the appropriate counter. The plugin receives a **counter interface** (injected by the server); it does not depend on Redis directly.

3. **OpenAI description plugin** (server/plugins/openai/description/plugin.go)

   - `instruments.description.openai.completion.model_not_found` -- model not found error (404 or model_not_found).
   - `instruments.description.openai.completion.quota_exceeded` -- quota exceeded error (429 or insufficient_quota).
   - `instruments.description.openai.completion.prompt_tokens` -- prompt token count (uses IncrBy).
   - `instruments.description.openai.completion.completion_tokens` -- completion token count (uses IncrBy).
   - `instruments.description.openai.completion.total_tokens` -- total token count (uses IncrBy).

4. **Massive.com plugin** (server/plugins/massive/client/client.go)

   - `instruments.identification.massive.request.succeeded` -- successful API request.
   - `instruments.identification.massive.request.failed` -- any error (network, status code, decode).
   - `instruments.identification.massive.request.rate_limit` -- HTTP 429.

### Counter interface (injected into plugins)

- Plugins must **not** depend on Redis. The server injects a small counter interface so that plugins can report metrics without importing Redis or the telemetry implementation.
- **Interface**: A single method, e.g. `Incr(name string)` or `Incr(ctx, name string)`, where `name` is the counter suffix (e.g. `instruments.identification.openfigi.mapping.succeeded`). The implementation (in the server or a shared telemetry package) prepends `portfoliodb:counters:` and calls Redis `INCR`.
- **Wiring**: When the server constructs or invokes plugins (e.g. identifier registry, ingestion worker), it passes an implementation of this interface. The ingestion worker also receives an implementation (backed by the same Redis client) for `instruments.resolution.totals.identify.attempts`. The OpenFIGI plugin receives the interface and calls it from `openfigi.go` after each Mapping/Search.

### API for the admin page

- New gRPC (or HTTP) admin-only method that returns a list of `{ name, value }` by scanning Redis for `portfoliodb:counters:*`. Counter names shown in the UI are the key suffixes (the part after the prefix).

---

## 2. Logger

### Overview

- **Output**: Standard out (stdout).
- **Level**: Controlled by an environment variable (e.g. `LOG_LEVEL`). For now use **debug** as the default so that OpenFIGI and identification paths are visible during debugging.
- **Behaviour**: When OpenFIGI is invoked (mapping or search), log at debug (or info) that we're calling the API (and optionally the input, e.g. ticker or query). On success, log success; on error, log the error (message and optionally status code / response body summary).

### LOG_LEVEL

- **Env var**: `LOG_LEVEL` (or `PORTFOLIODB_LOG_LEVEL` if we want to namespace).
- **Values**: At least `debug`, `info`, `warn`, `error`. Default: `debug`.
- **Implementation**: Use a standard Go logger that supports levels (e.g. `log/slog` from the standard library). Only emit a log line if its level is >= the configured level.

### OpenFIGI log points

- **On invoke**: When calling OpenFIGI mapping or search, log once per call with:
  - Operation: mapping or search.
  - Input: e.g. job ID/value for mapping, query (and optional exchCode) for search.
  - Level: debug (or info).
- **On success**: Log that the call succeeded and optionally result count (e.g. "mapping returned 1 result"). Level: debug.
- **On error**: Log that the call failed, with:
  - Error message (and for HTTP errors: status code, and optionally a short summary of the response body).
  - Level: error (or warn for rate-limit if we want to distinguish).

### Placement

- **Logger**: Initialized at server startup from `LOG_LEVEL`, stored in a package or passed where needed (e.g. to the OpenFIGI client or plugin).
- **Call sites**: In `server/plugins/openfigi/identifier/openfigi.go` (and optionally in `plugin.go` for "Identify started" / "Identify succeeded" / "Identify failed" if we want a single place for "OpenFIGI invoked"). Prefer one place (e.g. `Mapping` and `Search` in `openfigi.go`) so all HTTP-level outcomes are logged there.

### Dependencies

- Prefer **stdlib only** for T15: `log/slog` (Go 1.21+) is sufficient. No need for a third-party logging library unless we want structured JSON output to stdout; slog can do JSON or text.
