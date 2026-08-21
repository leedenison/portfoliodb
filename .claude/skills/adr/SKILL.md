---
name: adr
description: Architecture decision records for PortfolioDB, in docs/adr/ -- what an ADR is, when one is warranted, the file format and numbering, and how the spec references an ADR. Use when creating or updating an architecture decision record in docs/adr/, or when moving rationale out of docs/spec/ into an ADR.
---

# Architecture Decision Records

An ADR records *why* a decision was made. The spec (`docs/spec/`) states what
the system does; an ADR states why it does it that way. Keep the two separate:
rationale, rejected alternatives, and out-of-scope justifications belong in an
ADR, not the spec.

## Files and numbering

- ADRs live in `docs/adr/NNNN-slug.md` -- a 4-digit zero-padded number and a
  terse kebab-case slug (e.g. `0008-recharts-charting.md`).
- The next number is the highest existing ADR number plus one. Numbers are never
  reused.

## Format

Minimal. Title as an H1, then one to three sentences giving the context, the
decision, and the reason:

```markdown
# Charting library: Recharts

The front end needs a charting library for portfolio performance views. We use
Recharts for its React-native composable-component API, small bundle size, and
built-in responsive container, rather than a D3 wrapper.
```

An ADR can be a single paragraph. The value is recording *that* a decision was
made and *why* -- not filling in sections. Add these only when they earn their
place:

- **Considered Options** -- when the rejected alternatives are worth remembering.
- **Consequences** -- when non-obvious downstream effects need calling out.

## Status

An ADR has no frontmatter until another ADR replaces part or all of it. **The
absence of a status means accepted**; never write `status: accepted`.

When one arrives, the only field is `status`, and its value is one of:

```yaml
---
status: superseded by ADR-NNNN
---
```

- `superseded by ADR-NNNN` -- the whole decision is replaced.
- `partly superseded by ADR-NNNN` -- part of it is replaced; the rest stands.
- `deprecated` -- withdrawn with nothing replacing it.

An ADR that merely *amends* another gets no status field. Record the
relationship as a sentence at the top of the amended ADR's body instead
(`Amended by [NNNN](NNNN-slug.md), which ...`), saying what changed and what
stands.

## Superseded ADRs are stubs

Numbers are never reused, so a superseded ADR keeps its file, its number and its
title -- the spec and the issues point at them. Cut the body to the status, a
one-line pointer to the successor, and **only the reasoning the successor does
not carry**. Do not leave the original argument standing beside the replacement:
a reader who finds the same thing argued twice cannot tell which is current.

## Compression

An ADR records the decision that holds now and why. It is not a changelog.

- Do not narrate how the decision was reached -- earlier drafts, what was tried
  first, which release fixed it. If history taught something, state the lesson
  in the present tense; if it was a plain mistake, delete it.
- Do not restate another ADR at length after linking to it. Name what you are
  relying on in a clause and link.
- Fold an amendment into the body it amends rather than appending an
  "Amendments" section. The body should read as one current decision.
- Reference other ADRs as `[NNNN](NNNN-slug.md)` and issues as
  `issue [NNNN](../issues/NNNN-slug.md)`. A bare `0053` is ambiguous -- ADR 0053
  and issue 0053 both exist.

## When an ADR is warranted

All three must hold:

1. **Hard to reverse** -- changing your mind later carries real cost.
2. **Surprising without context** -- a future reader will wonder "why on earth did they do it this way?"
3. **The result of a real trade-off** -- there were genuine alternatives and one was chosen for specific reasons.

If a decision is easy to reverse, unsurprising, or had no real alternative, skip
the ADR.

## Referencing an ADR from the spec

When a spec doc needs to point at the reasoning behind a behaviour, use a terse
pointer only -- `(see adr/NNNN-slug.md)` -- never restate the rationale in the
spec. The behaviour lives in the spec; the reasoning lives in the ADR.

## Relationship to grill-with-docs

The global `grill-with-docs` skill *offers* ADRs opportunistically during a
plan-grilling session and defines the same file format. This skill governs the
format and standalone creation of ADRs in this repo; the two agree on layout and
numbering. For issues and milestones see the `issue-tracker` skill.
