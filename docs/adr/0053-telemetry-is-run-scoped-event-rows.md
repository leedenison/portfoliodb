# Telemetry is run-scoped event rows, not counters

[0009](0009-telemetry-and-logging.md) made telemetry a set of Redis integers under a
dotted namespace. The naming convention was the whole of the design: nothing recorded
what a counter counted one of, and nothing related two counters to each other.

That is why those counters cannot be read.
`instruments.resolution.totals.description.attempts` is incremented once per batch
item, `...no_hints` once per batch, and `...plugin_error` once per plugin per batch --
three grains sharing one namespace, so no two of them can be added, subtracted or
divided. Nothing says whether a pair of counters partitions a population or overlaps
it. And no counter is attributable: having imported a file, there is no way to ask what
happened during that import, because the numbers are global running totals with no
notion of when or on whose behalf they moved.

Counters are replaced by event rows in Postgres, read by a Grafana container. One rule
governs every table:

> One row is one completed unit of work, and carries exactly one outcome drawn from a
> closed, mutually exclusive vocabulary.

The vocabulary being closed is what makes a chart interpretable without knowing how the
code branches; the unit of work being nameable is what stops a second grain being
smuggled into the same table.

## A run is the unit everything hangs off

Work is either an ingestion job or a worker cycle, and identification runs under both
-- from tx import, from the price worker, from archive declarations, and from the
corporate events cycle that adjusts option identity across splits. A `job_id` would
leave that last one parentless and invisible.

So a **run** is one activation of one subsystem, of which a job and a cycle are two
kinds. Every event row carries a non-nullable `run_id`. This subsumes what would
otherwise be a separate worker-runs metric: the run table is that chart.

A run row is created before its children, because they reference it, and stamped with
its outcome when it ends. It is therefore written twice, and a run whose process died
never receives the second write. `incomplete` is an explicit member of the outcome
vocabulary rather than a null, so that a killed container reads as a fact rather than a
hole -- which under the dev stack's pinned `memswap_limit` is a diagnosis, not an edge
case.

A job's status is denormalised onto its run rather than joined from `ingestion_jobs`.
The duplication is accepted because a run's outcome is a historical fact once stamped,
and because it keeps every panel reading one table regardless of run kind.

An RPC request is deliberately **not** a kind of run. No handler does substantial
synchronous work -- the trigger RPCs poke a channel and return, the import RPCs enqueue
a job -- so nothing would populate it, while the SPA's polling would swamp the run
table and make the run-kind chart unreadable. `TriggerGrouping` is the illustration: it
drops the trigger when the channel is full and still answers OK, so the request outcome
and the work outcome can disagree outright.

## Grains nest; they do not sum

Under a run, per distinct resolution key -- one (source, description, hints) triple,
not one transaction -- sit description extraction and identification, each with its own
plugin calls. A cache hit is not an outcome in any of these tables: it is an artefact of
deduplication, and counting it would make the population transactions rather than
descriptions.

Relationships between grains are foreign keys, not arithmetic identities. Asking whether
buckets sum was forced by Redis having nothing but scalars; a resolution key producing
four identification attempts is a fact recorded in a `purpose` column rather than a
discrepancy between two totals.

Each table is exposed through one wide view that flattens its parents in and never fans
out into its children, because a view spanning two sibling grains duplicates the
parent's rows and makes counting it silently wrong. Judgements live in the view as
computed columns -- which outcomes count as having reached a plugin, which runs are
imports -- and panels select on them, so a definition is stated once in reviewed SQL
rather than retyped into each dashboard.

## Plugins report telemetry; the orchestrator writes it

0009 injected a counter interface into plugins so they would not depend on Redis. That
stays true in spirit and changes in mechanism, because a single plugin-call row needs
facts from two parties that never meet. Only the plugin knows its transport outcome,
its retries and its token usage. Only the orchestrator knows whether that plugin won,
was superseded by a better hint match, or was discarded as inconsistent with the winner
-- decided after every plugin has returned, and unknowable to any of them.

So `Identify` and `ExtractBatch` return a telemetry value alongside their result, and
the orchestrator composes and writes the row. The alternative -- the plugin writing its
own row and the orchestrator updating it -- makes plugins depend on the telemetry
backend and drags a database into every plugin test, which is the thing 0009 existed to
prevent. Plugins consequently stop depending on a telemetry interface at all.

## Consequences

The Redis counter infrastructure is retained but is left with no defined counters and
no callers, so that reintroducing a counter is a matter of calling it rather than
rebuilding it. The admin telemetry page and the `ListTelemetryCounters` RPC behind it
lost their subject matter and are removed, along with the prefix scan that only ever
served that page. Nothing in the SPA reports telemetry: it is read in Grafana.
