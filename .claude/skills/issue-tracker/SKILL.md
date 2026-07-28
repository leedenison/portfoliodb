---
name: issue-tracker
description: The PortfolioDB issue tracker in docs/issues/ -- one markdown file per issue plus a thin milestones.md. Covers file naming and numbering, the issue frontmatter schema (status, title, milestone, dependencies), milestone labels, and how to open, close, and depend issues. Use when creating, closing, or updating issues or milestones in docs/issues/.
---

# Issue Tracker

Project issues live as one markdown file per issue under `docs/issues/`, with a
single `docs/issues/milestones.md` holding the milestone labels. There is no
external tracker; these files are the source of truth. Keep them terse -- an
issue body is usually a sentence or two.

# Creating Issues

Create issues for coarse grained, feature level issues (eg. "Enable transaction
CSV upload for browser extensions").  Do not create fine grained implementation
issues.

Users will create issues for bugs which may be fine grained.

## Files and numbering

- Issues are `docs/issues/NNNN-slug.md` -- a 4-digit zero-padded number and a
  terse kebab-case slug derived from the title (e.g. `0017-filter-instrument-export.md`).
- The next number is the highest existing issue number plus one. Numbers are
  never reused, even after an issue is closed.
- `docs/issues/milestones.md` is the only non-issue file in the directory.

## Issue format

Minimal YAML frontmatter, then a markdown body that describes the issue:

```markdown
---
status: open
title: Filter instrument export by broker and exchange
milestone: M06
dependencies: [0016]
---

Filter instrument export by broker and exchange.
```

Frontmatter fields:

- `status` (required) -- `open` or `closed`.
- `title` (required) -- a single line; the slug is derived from it.
- `milestone` (optional) -- a milestone label (`M06`); omit for unscheduled issues.
- `dependencies` (optional) -- a list of issue numbers this issue depends on
  (`[0029, 0030]`); omit when there are none. Reference issues by number, not slug.

The body is free-form markdown describing the work. Do not restate the
frontmatter in the body.

## Working with issues

- **Create:** allocate the next number, write the file with `status: open`.
- **Close:** edit `status: closed` -- do not delete or renumber the file.
- **Reopen:** edit `status: open`.
- **Depend:** add the dependency's issue number to the `dependencies` list.

## Milestones

`docs/issues/milestones.md` lists milestone labels (`M01`, `M02`, ...) each with
a one-sentence description, followed by an `## Unscheduled` section listing
future milestone ideas that have no label yet. Milestones carry no status of
their own -- completion is read from the `status` of the issues assigned to them.
Add a new milestone by appending the next `M`-number; promote an unscheduled idea
by giving it a label and moving it into the main list.

For architecture decision records see the `adr` skill; issues track work, ADRs
record why decisions were made.
