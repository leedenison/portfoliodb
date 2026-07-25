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

- **Status** frontmatter (`proposed | accepted | deprecated | superseded by ADR-NNNN`) -- when a decision is revisited.
- **Considered Options** -- when the rejected alternatives are worth remembering.
- **Consequences** -- when non-obvious downstream effects need calling out.

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
