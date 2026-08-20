---
status: open
title: Manually correct a misidentified instrument
milestone: M21
dependencies: [0066, 0138]
---

Let a person say that a row resolved to the wrong instrument, and have the
correction stick.

## Motivation

0104 covers the row that could not be resolved at all: it announces itself with a
placeholder and an identification error, and the repair is to supply the answer
the system lacked. A row that resolves to the *wrong* instrument says nothing.
Nothing detects it, and there is no surface on which to disagree with it.

The ways it happens are ordinary. An undomained `MIC_TICKER` matches the wrong
listing, a reused CUSIP carries a historical transaction to the wrong issuer, an
OCC name is recorded under the wrong vintage and its validity interval covers a
period it was never live (0124, 0125). The last of those does not present as a
wrong instrument at all -- it presents as a price series that quietly fetches the
wrong contract.

Identity divides by who owns it, and the correction divides with it. A user's own
attribution is theirs to fix; the shared identifier mapping behind it is an
admin's, and one correction there lands on every user.

## Open

How any of this happens is unsettled, and left so deliberately:

- What a person corrects. 0138 answers half of it: the row records the identifier
  resolution was given, and a correction is a user-owned identifier with the same
  name rather than a patch to the row. What is still open is the surface -- what
  a person is shown and what they press.
- Whether a user's correction is an override scoped to their own data or a
  request for an admin to change shared reference data. 0138 makes both possible
  and argues the answer differs by what resolved the row -- a broker description
  is one user's reading of one source and is theirs, an ISIN is not.
- What keeps re-identification from undoing a correction. Surviving the next
  ingestion is answered by 0138: an override that is an identifier is re-derived
  onto rather than remembered, where a per-row marker would be lost to the
  delete-and-rewrite an upload performs.
- What correcting means now that identity is an interval rather than current
  state (0125). Closing a name at a date and minting its replacement is the
  operation `ApplyOptionSplit` performs, and doing it by hand needs a shape.
- Whether wrongness can be surfaced at all, or whether this only ever starts with
  a person noticing.
