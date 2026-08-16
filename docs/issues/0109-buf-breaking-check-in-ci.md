---
status: open
title: Check proto compatibility in CI once the wire format is stable
---

Add a `buf breaking` check to CI, run against the proto definitions on `main`,
and drop the pre-release exemption in CLAUDE.md that lets a retired field's
number be reused.

## Motivation

Nothing today stops a change from renumbering a field, retyping one, or reusing
a number a retired field used to hold. While the project is pre-release that is
deliberate: no deployed reader outlives a deployment, no encoded protobuf is
persisted across one, and CLAUDE.md says to renumber a message rather than
reserve the numbers it has vacated. The cost of that freedom is that the only
thing catching an accidental incompatibility is review.

That stops being the right trade at the first release, and the change is easy to
miss because nothing fails when it is due -- the check simply is not there.

## Scope

- A `buf breaking --against` step in `.github/workflows/ci.yml`, comparing the
  branch against `main`. `buf.yaml` already configures the workspace; the
  breaking rule category (`WIRE` or `WIRE_JSON`) has to be chosen.
- `WIRE_JSON` is the one to weigh carefully: archive documents are protojson
  keyed by field name, so a rename is breaking for a stored archive even though
  the wire encoding does not notice.
- Reinstating `reserved` for retired fields, and reverting the paragraph in
  CLAUDE.md's Project Status section that currently forbids it.
- What to do about the numbering already vacated. Every message was renumbered
  from 1 at the point the exemption was written, so the baseline is whatever
  `main` holds when the check lands, not the history behind it.
