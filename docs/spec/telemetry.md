# Telemetry

## 1. Events

Telemetry is event rows in a `telemetry` schema in the application database, read
by a Grafana container through a SELECT-only role. See
[adr/0053](../adr/0053-telemetry-is-run-scoped-event-rows.md) for why, and for what
replaced the Redis counters.

One rule governs every table below:

> One row is one completed unit of work, and carries exactly one outcome drawn from
> a closed, mutually exclusive vocabulary.

Where a row has two outcome columns it is because the unit of work has two sequential
stages, not because two grains share a row. Counts of one grain are never derived by
adding counts of another; grains relate by foreign key.

### Shape

```
run
├── resolution_key                     one distinct (source, description, hints)
│     └── identification_attempt       one ResolveWithPlugins call
│           └── identifier_plugin_call one plugin invocation
└── description_plugin_call            one plugin invocation over a batch
```

`description_plugin_call` hangs off the run rather than off a resolution key: one
`ExtractBatch` call covers many descriptions at once, so it has no single parent key.
Identifier plugins are called once per plugin per attempt and do nest. This asymmetry
is forced by the code and must not be flattened away -- it is what made the counters
it replaces impossible to add up.

### run

One activation of one subsystem. Created before its children and stamped when it ends.

| column | notes |
| --- | --- |
| `id` | referenced by every event row |
| `kind` | `tx_import`, `user_archive_import`, `system_archive_import`, `grouping_cycle`, `transfer_match_cycle`, `corporate_event_cycle`, `price_fetch_cycle`, `inflation_cycle` |
| `job_id` | `ingestion_jobs.id` when the run is a job; null for a cycle |
| `user_id`, `broker`, `source` | null for cycles |
| `started_at`, `ended_at` | |
| `outcome` | `success`, `failed`, `incomplete`; null while in flight |
| `telemetry_incomplete` | a telemetry write failed, so this run's counts understate |

`incomplete` means the run died. It is stamped by a sweep at service startup over runs
with no terminal outcome, which is what lets a null outcome mean genuinely running now.
The sweep leaves `ended_at` null, because the run ended when its process died and
nothing recorded when that was; a run stamped `incomplete` therefore has no duration.
`telemetry_incomplete` is unrelated to it: the work may have succeeded while its
telemetry was lost, and a panel should mark such a run rather than trust its counts.

The three import kinds are the three ingestion job types. The five cycle kinds are the
five triggered workers, and a cycle that finds no work still opens a run and stamps it
`success`: it ran, and a run table with holes in it is harder to read than one with
quiet rows.

### resolution_key

One distinct `(source, instrument_description, identifier hints)` triple within a run --
the thing `cacheKeyWithHints` names. **Not** one transaction: many transactions share a
key and resolve once. A cache hit is not an outcome, because it is not a resolution;
`tx_count` records the fan-out instead, so a failure affecting 300 rows can be told from
one affecting 1.

| column | notes |
| --- | --- |
| `run_id`, `source`, `description` | |
| `tx_count` | transactions sharing this key |
| `had_identifier_hints`, `security_type_hint`, `instrument_kind` | lets a spike be attributed rather than merely noticed |
| `extraction_outcome` | stage 1, below |
| `outcome` | stage 2, below |
| `mismatch_detected` | MIC_TICKER and OPENFIGI_SHARE_CLASS resolved differently |
| `instrument_id` | null when unresolved |

`extraction_outcome`: `hints_found`, `no_hints`, `not_attempted_db_hit`,
`not_attempted_hints_supplied`, `not_attempted_type_filter`, `not_attempted_no_plugins`.
The `not_attempted_*` members are where skips live. Extraction is skipped for a
description already resolved by DB lookup, and for one whose every posting names an
identifier, because extraction exists to find an identifier and is a paid call.

`outcome`: `db_source_description`, `db_identifier_hints`, `identified`,
`broker_description_only`, `extraction_failed`, `plugin_timeout`, `plugin_unavailable`,
`conflicting_hints`. The two `db_*` members are distinct lookups -- by stored
`(source, description)` and by supplied identifier hints -- and conflating them hides
which path is carrying an import. The three fallback members mirror the messages the
resolver already records against a row.

`mismatch_detected` is a flag rather than an outcome. Resolution continues and succeeds
using MIC_TICKER, so a mismatch is not a terminal state, and modelling it as one would
make the outcome column non-exhaustive.

The price and corporate event parts of an archive resolve instruments through the same
resolver, but from an identifier and no broker description. They still write a key,
because an identification attempt reaches its run through one. The identifier names it
-- `description` is `TYPE:DOMAIN:VALUE` and `source` is empty, an archive being no
broker's export -- and `tx_count` carries the archive groups sharing it, the fan-out
this grain records whatever the things sharing it are called. `extraction_outcome` is
`not_attempted_hints_supplied` for the reason a posting carrying an identifier skips
extraction, and an instrument ensured from the supplied identifier alone is
`broker_description_only`: no plugin resolved it and the row's own contents are what
the instrument was built from, which is that member's shape.

### identification_attempt

One `ResolveWithPlugins` call. A single resolution key produces several: one `primary`,
two more when the mismatch check runs, and one per level of underlying recursion.

| column | notes |
| --- | --- |
| `resolution_key_id` | |
| `purpose` | `primary`, `mismatch_check`, `underlying` |
| `depth` | recursion depth; 0 for the first call |
| `outcome` | `db_short_circuit`, `no_eligible_plugins`, `identified`, `not_identified`, `plugin_timeout`, `plugin_error` |
| `security_type_hint`, `asset_class`, `had_identifier_hints` | |

A plugin filtered out by acceptable kind or security type produces no
`identifier_plugin_call` row, because no call was made. When that filter removes every
plugin the attempt records `no_eligible_plugins`.

`asset_class` is the class the attempt landed on, against `security_type_hint`, which is
the class it was asked about. An attempt that identified nothing has no class to record.

A resolution that fails outright -- a database read that errored rather than a plugin
that found nothing -- writes no attempt row. `outcome` is not nullable, and there is no
member for a unit of work that did not complete; the run's own outcome is where that
failure shows.

### identifier_plugin_call

One plugin invocation within an attempt.

| column | notes |
| --- | --- |
| `identification_attempt_id`, `plugin_id` | |
| `outcome` | `won`, `superseded`, `discarded_inconsistent`, `not_identified`, `rate_limited`, `timeout`, `error`, `skipped_expired` |
| `retries`, `duration_ms` | |

The first three are all successes and are decided by the orchestrator after every plugin
has returned: `superseded` lost to a better hint match despite higher precedence, and
`discarded_inconsistent` was dropped as contradicting the winner. A plugin cannot know
either, which is why the plugin returns its transport outcome and the orchestrator
composes the row.

### description_plugin_call

One plugin invocation over a batch.

| column | notes |
| --- | --- |
| `run_id`, `plugin_id` | |
| `precedence` | where this plugin sat in the chain, higher first |
| `batch_size` | items passed to this plugin, after the type filter |
| `items_with_hints` | items it returned hints for |
| `outcome` | `hints_returned`, `no_hints`, `error`, `rate_limited`, `quota_exceeded`, `model_not_found` |
| `prompt_tokens`, `completion_tokens`, `total_tokens` | null for plugins with no token cost |
| `duration_ms` | |

Description plugins run in precedence order and each sees only the items its
predecessors failed on, so `batch_size` is a different population per plugin and rates
are not comparable between them. Identifier plugins run in parallel and every eligible
plugin is called, so those rates are comparable. Tokens are columns rather than running
totals, which is what makes the cost of one import answerable.

`precedence` is what makes that narrowing readable: without it the order the batches
shrank in cannot be recovered from the rows, and `batch_size` descending is only a guess
at it, arbitrary the moment two plugins are handed equal batches. It is the plugin's
configured precedence rather than an ordinal of the rows written, because a plugin whose
filtered batch is empty writes no row at all -- so a gap in the sequence means that
plugin was skipped, which is how a filtered-out identifier plugin already reads.

### Views

One view per table, each flattening its parents in and never fanning out into its
children -- a view spanning two sibling grains duplicates the parent's rows and makes
counting it silently wrong.

Judgements are computed columns on the view; selection belongs to the panel. A judgement
a panel needs and no view carries is a gap in the views, not a licence to inline an
outcome list into a dashboard. They are:

| column | on | meaning |
| --- | --- | --- |
| `is_import` | every view | the run kind is one of the three import kinds |
| `reached_plugins` | attempt, identifier call | the attempt outcome is neither `db_short_circuit` nor `no_eligible_plugins` |
| `resolved` (`key_resolved` on children) | key, attempt, identifier call | the key ended holding a real identifier |
| `had_attempt` | key | the key produced at least one identification attempt |
| `transport_failed` | identifier call | the call did not complete |
| `call_failed` | description call | the call produced no answer |

`reached_plugins` is the denominator for identification failure rate. Using all attempts
instead makes the rate fall as the instrument table fills, because more resolutions
short-circuit in the database, which reads as improving identification when nothing has
changed. A failure-rate panel filters `purpose = 'primary'` as well, or an import
carrying more dual-hint descriptions inflates the denominator with mismatch-check
attempts.

`resolved` covers `db_source_description`, `db_identifier_hints` and `identified`, and
deliberately excludes `broker_description_only`: nothing identified that instrument and
the row's own contents are all it was built from, which is a failure for a transaction
import however ordinary it looks. It is the expected outcome for an archive run, so a
panel charting the complement splits by run kind rather than blending the two. A null
outcome counts as not resolved rather than as null, or an unstamped key would drop out of
both the column and its negation. `instrument_id IS NOT NULL` is not the same test and is
not a substitute: an archive key ensured from a supplied identifier has an instrument and
identified nothing.

`had_attempt` separates a key that never asked from one that asked and was told nothing.
Four of the five paths that stamp a key return before `ResolveWithPlugins` is called, so
no attempt is ordinary rather than a fault.

`transport_failed` and `call_failed` both draw the line at the call completing.
`not_identified` and `no_hints` stay outside them: an empty answer is an answer, and
counting it as a fault makes a plugin that correctly knew nothing read as a plugin that
is down.

`v_run` also carries `key_count`, `key_tx_count` and `description_call_count`. These are
scalar subqueries, one per child table, and they are the only sanctioned way for a view
to reach into a second child. The rule above forbids a view *fanning out* into two
sibling grains, because that repeats the parent once per child and makes counting it
wrong; an aggregate repeats nothing and `v_run` stays one row per run. A panel wanting
per-run child counts uses these and never a join.

`key_tx_count` is transactions that needed resolution, not rows in the imported file: a
row naming no instrument never becomes a key. It is the closest thing to an import's size
the schema holds, and it is the denominator every rate over resolution keys wants. A
transaction count on `run` itself is deliberately not recorded -- it would mean postings
for one import kind, heterogeneous rows processed for another, nothing for the third and
null for every cycle, which is a column meaning a different thing per row and the grain
confusion this schema exists to end. The imported file's own row count is not available
at all: `run.job_id` is not a foreign key and `ingestion_jobs` is outside the reading
role, both by design.

### Naming an instrument

A resolution key records `instrument_id` and nothing readable, which leaves no panel able
to say which instrument a description landed on -- the question asked after every manual
import, since a description resolving to the wrong listing looks identical to it
resolving to the right one in a bare UUID.

Recording a label when the key is stamped was rejected: the readable instrument is two
frames below the write site and deliberately discarded, and three of the five paths that
stamp a key never hold one, so the column would be null exactly where identification was
most interesting.

So `telemetry.v_instrument_label` is a lookup instead. A view runs with its owner's
privileges rather than its caller's, so granting SELECT on it lets the reading role turn
an id into a name while still holding no privilege on `instruments` and no USAGE on
`public`. That indirection is the point: one narrow window, reviewed in the migration,
rather than a grant on the application schema. It is the only place the telemetry schema
reads outside itself.

It is a live lookup and not a recorded fact -- an instrument renamed today changes what a
panel says about a run from last year -- which is why it is a separate view and is joined
by a panel rather than folded into the views above.

### Writing

Writes are synchronous, through a connection pool separate from the application's, and
confined to `server/db` as all SQL is. They never join the work's transaction: a failed
import rolls back, and telemetry riding along would erase the diagnostics for the run
most worth inspecting. A write that fails sets `telemetry_incomplete` on the run and is
otherwise ignored -- telemetry never fails the work. No write returns an error, so a
caller threads ids through without testing them; a write whose parent id is missing is
skipped, which costs one failure its own subtree and nothing else.

`run` and `resolution_key` are each written twice -- created with what is known when the
work starts, stamped with its outcome when the work ends. For a run that is what leaves
one whose process died unstamped. For a resolution key it is forced by the foreign key:
its identification attempts reference it, so it has to exist before its own outcome is
known. The other three grains are written once, when their unit of work completes, which
for an identifier plugin call is the moment the orchestrator decides `won` against
`superseded` against `discarded_inconsistent`.

Plugins do not write. `Identify` and `ExtractBatch` return a telemetry value alongside
their result and the orchestrator composes the row, so no plugin depends on the
telemetry backend or needs a database to unit test.

### Retention

Plain tables, not hypertables: the volume does not warrant Timescale, and foreign keys
into a hypertable are an avoidable complication. Children cascade from `run`, so
retention is a delete over `run.started_at`, at **360 days**. Traffic is low and a
problem may go unnoticed for a long time, so the window is set to outlast the gap
between a regression landing and someone looking for it.

The delete is exposed as an admin RPC and nothing in the service schedules it. The
service runs no cron of its own, and a retention window measured in a year is not worth
one: an operator's scheduler calls the RPC, and the count it answers with is what that
scheduler logs. This is why the RPC is synchronous and returns what it deleted, unlike
the `Trigger` RPCs, which poke a worker and answer immediately.

The schema, its tables, its views and the SELECT-only grants live in
`server/migrations/005_telemetry.sql`, separate from the application schema because it is
a separate scope rather than a later change to the same one.

The reading role's password is not in the migration, which rules out the migration
creating the login the dashboard uses. So the migration creates `telemetry_reader`, a
NOLOGIN group role holding SELECT and nothing else, and the login role is created by
`docker/postgres/init/10-telemetry-reader.sh` from compose environment and granted
membership of it. The privileges are then reviewed in the repository and the password
never is.

That script runs from the Postgres entrypoint, on an empty data directory, before the
service has connected and so before the migration has run. It therefore creates
`telemetry_reader` itself; the migration guards its own `CREATE ROLE` on `pg_roles` and
adds only the grants. The apparent duplication is what the ordering costs. The test and
e2e stacks mount no init script and take the role from the migration alone, which is why
the privilege test needs no login to run against.

## 2. Dashboards

Grafana runs as a container in the dev stack, bound to loopback, with its datasource and
its dashboards provisioned from files under `docker/grafana`. Dashboards are read only in
the browser: an edit saved there would live in the container volume and drift from the
repository unnoticed.

There are two, over the same tables. One is scoped by a run picker, for reading a single
import during manual testing; the other buckets over time, for drift. Panels select from
the views and carry no definitions of their own -- `where is_import and reached_plugins`,
never a repeated list of excluded outcomes. A judgement a panel needs and the views do not
carry is a gap in the views.

## 3. Logger

### Overview

- **Output**: Standard out (stdout).
- **Level**: Controlled by an environment variable (e.g. `LOG_LEVEL`). The default is currently **debug** (see adr/0009-telemetry-and-logging.md).
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

- Use the standard library `log/slog` (Go 1.21+); no third-party logging library (see adr/0009-telemetry-and-logging.md).
