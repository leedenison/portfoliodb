---
status: open
title: Export and import portfolio definitions
dependencies: [0078, 0080]
---

Carry `portfolios` and `portfolio_filters` across a rebuild. Deliberately
excluded from the first user archive.

## Motivation

A portfolio is a saved view over transactions, defined by filters the user wrote
(adr/0010-portfolios-as-views.md). Nothing exports it and nothing can rebuild
it, so a rebuilt instance loses every portfolio definition. It is tier 1 in
adr/0032-archive-preserves-inputs-not-derived-state.md.

It is deferred rather than included because it is the one piece of user data that
does not sit cleanly on one side of the archive split.

## Why it is harder than it looks

`portfolio_filters.filter_value` is text holding a broker name, an account
string, or an **instrument UUID** when `filter_type` is `instrument`. That last
case is a reference from user-owned data into admin-owned data, so it cannot be
exported as stored: the id is meaningless in another instance. It has to be
translated to an identifier on the way out and resolved back on the way in,
which brings with it the failure modes 0076 and 0077 already carry -- an
identifier the resolution path cannot take back, and a merge moving one between
instruments.

Shared portfolios, which aggregate over several users' data and sit in the
unscheduled list, widen the same seam: a portfolio would then reference data the
exporting user does not own, and the archive boundary in
adr/0033-system-and-user-archives-are-separate.md would need an answer for it.

## Why it is deferred rather than scheduled

The general idea is agreed -- a portfolio definition is user-owned data and a
rebuild should not lose it. What is not agreed is the definition itself. The
filter model it would serialise is the current one rather than a settled one, and
committing it to an archive format fixes it before it is ready: shared portfolios
and tag-based definitions both sit in the unscheduled list and both change what a
definition is.

So the archive is completed without it, and a user rebuilding an instance writes
their portfolios again by hand. That is a handful of filter rows entered once,
against a format that would otherwise have to be migrated when the definition
settles.

Take it up once the portfolio definition is pinned down, and once the identifier
translation above has been exercised by transactions and declarations.
