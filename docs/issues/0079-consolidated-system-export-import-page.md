---
status: closed
title: Consolidated system export and import page
dependencies: [0078]
---

One admin page that exports and imports the system archive, replacing the
per-entity controls scattered across the admin section.

## Motivation

Export and import are currently an affordance on whatever page happens to own
the entity: a download button on `/admin/prices`, another on
`/admin/instruments`, another on the splits tab of `/admin/corporate-events`,
each with its own import modal. Rebuilding an instance means remembering which
pages have one and clicking through them in the right order.

The archive is a single artefact (adr/0033-system-and-user-archives-are-separate.md),
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

Closed. `/admin/archive` exports and imports the system archive, and the three
per-entity affordances are gone.

Two things landed differently from the description. The three parts the archive
format does not carry yet -- inflation indices, fetch blocks, unhandled event
resolutions, plugin config -- ship as disabled menu rows rather than as working
entries; 0086 enables them, and the note about plugin config carrying live API
keys lives on the row until it does. And the per-entity export and import
endpoints were replaced rather than merely hidden: there is now one export RPC
taking a menu of parts and one import RPC taking a whole document and sequencing
it server-side, so an import survives the admin closing the tab.
