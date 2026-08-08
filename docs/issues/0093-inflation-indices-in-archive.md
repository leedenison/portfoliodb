---
status: closed
title: Carry inflation indices in the system archive
milestone: M14
dependencies: [0078]
---

Export and import the monthly inflation index series as a system archive part.

## Motivation

Inflation indices are the fourth part the archive format was always meant to
carry and the only one with no issue of its own. They are tier 2 in
adr/0032-archive-preserves-inputs-not-derived-state.md -- refetchable in
principle, through the same paid and rate-limited providers the archive exists to
avoid paying twice.

There is a wrinkle that makes them less refetchable than the tier suggests. A
revision replaces its predecessor in place and leaves no record
(docs/spec/bitemporality.md), so a refetch returns the current revision rather
than the values an instance was valuing against. Losing them is not only a cost.

Left undone, `/admin/archive` keeps a disabled row saying the format does not
carry them yet, which is what 0079 recorded and what 0086 removes for the other
three.

## Design

A part of the system archive, grouped by currency.

Unlike the price and corporate event parts, a group carries no coverage: a series
is dense, `inflation_indices` stores no coverage of its own, and a file must not
claim more than the table it came from can answer for. `base_year` is on the row
rather than on the group, because a rebasing changes it partway through a series
and both halves travel in the one group.

Closed. Inflation indices are a system archive part, grouped by currency and
carrying no coverage.
