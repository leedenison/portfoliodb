---
status: open
title: Grafana container reading the telemetry schema
milestone: M20
dependencies: [0115]
---

Add a Grafana service to the dev stack with its datasource and dashboards provisioned
from files in the repo, connecting to the `telemetry` schema as the SELECT-only role.
Bind it to localhost.

Two dashboards over the same tables: one scoped by a run picker, for reading a single
import during manual testing, and one bucketed over time for drift. Panels select from
the views and carry no definitions of their own -- `where is_import and reached_plugins`
rather than a repeated list of excluded outcomes.
