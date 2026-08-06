---
status: open
title: Consolidated admin export and import page
dependencies: [0078]
---

One admin page that exports and imports the admin archive, replacing the
per-entity controls scattered across the admin section.

## Motivation

Export and import are currently an affordance on whatever page happens to own
the entity: a download button on `/admin/prices`, another on
`/admin/instruments`, another on the splits tab of `/admin/corporate-events`,
each with its own import modal. Rebuilding an instance means remembering which
pages have one and clicking through them in the right order.

The archive is a single artefact (adr/0033-admin-and-user-archives-are-separate.md),
so it should have a single place to produce and consume it.

## Design

A menu of what to include, since not every export wants everything: instruments,
prices, corporate events, inflation indices, fetch blocks, unhandled event
resolutions, plugin config.

`plugin_config` is **off by default**. Including it carries live API keys, which
makes the archive a secret and changes where it can safely be stored; that
should be a deliberate choice rather than the default. See 0086.

Import reads the parts in dependency order and reports per-part results.

Remove the separate export and import affordances from `/admin/prices`,
`/admin/instruments` and `/admin/corporate-events` rather than leaving two ways
to do it. Update docs/spec/information-architecture.md.

The user archive has its own page under 0080. The two never mix: no user data is
reachable from this page.
