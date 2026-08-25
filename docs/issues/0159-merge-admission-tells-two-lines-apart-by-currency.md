---
status: open
title: Merge admission tells two lines apart by currency
milestone: M25
dependencies: [0155]
---

`contradicts` treats two identifier-plugin results naming one symbol at two
venues as having named two listings, and `consistentWith` excludes the loser from
the merge on that basis. But a line is keyed on its currency
(adr/0068), so two venues quoting one currency are one listing and the venue
there stands in for the currency rather than deciding on its own account.

Merge admission is rightly strict -- it compares two answers rather than an
answer against a partial store, so the open-world venue rule
(adr/0077) does not reach it -- but it should be strict on the
field that decides identity. Two results agreeing on a currency and differing
only on venue have described one line.

Turned up while retiring the security's own exchange in 0155, which is where the
strict and permissive readings of a venue were separated and labelled.
