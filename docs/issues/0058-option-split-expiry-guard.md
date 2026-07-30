---
status: open
title: Do not retroactively restate options that expired before the split
---

`ListPendingOptionSplits` selects options whose identity predates an effective
split on the underlying, without checking whether the option was still live on
the ex_date.

## Motivation

The predicate is `ex_date <= CURRENT_DATE AND (identity_as_of IS NULL OR
identity_as_of < ex_date)`. An option that expired *before* the ex_date satisfies
it, so `ApplyOptionSplit` rewrites its OCC identifier and strike to a contract
that never traded. OCC restates a contract on the effective date; one that had
already expired was never restated.

Importing a pre-split option price file makes this reachable, since the file's
`exported_at` puts `identity_as_of` before the ex_date by design -- that is how
the genuinely-restated contracts get picked up. Two NVDA puts expiring
2024-03-15, before the 2024-06-10 split, would be rewritten from strikes 420 and
510 to 42 and 51.

Distinct from 0055, which concerned which clock the guard compares rather than
which options it selects.

## Design

Add the expiry to the predicate. `instruments.expiry` is already NOT NULL for
options via the check constraint in 001_initial.sql, so no schema change is
needed.

Consider whether an option expiring on the ex_date itself should be adjusted:
OCC restates it, and it can still be exercised that day, so the comparison is
likely `expiry >= ex_date` rather than `expiry > ex_date`.
