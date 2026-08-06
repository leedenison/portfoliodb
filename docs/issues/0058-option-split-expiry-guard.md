---
status: closed
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

## Resolution

`ListPendingOptionSplits` gained a third guard, `s.ex_date <= o.expiry`. It sits
alongside the future-date guard and the correctness guard and needed no schema
change: `chk_option_fields` already makes `expiry` NOT NULL for every option. The
comparison is inclusive because OCC restates a contract on the morning of the
effective date and it remains exercisable that day. Because the guard applies per
joined row, an option that lived through one split and expired before the next is
returned pending for the first alone.

The same bound was applied to `AdjustOCCForKnownSplits`, which rebased an OCC
hint by every split between the hint's vintage and today regardless of expiry.
Fixing only the query would have been worse than fixing neither: the rebased hint
is what `ResolveByHintsDBOnly` looks up, so a later broker import would have
searched for a post-split symbol, missed the stored row the pass now correctly
leaves alone, and created a duplicate instrument. Rebasing now stops at
`min(now, expiry)`.

That made the `OCC_AT_EXPIRY` internal hint redundant -- it existed to give
OpenFIGI the symbol an expired contract wore at expiry while the ordinary OCC
hint ran on to today -- so it was removed along with the resolver skip clauses,
its OpenFIGI id-type mapping, and the block that overwrote a returned OCC with
the rebased one.

Recorded in docs/adr/0036-expired-options-are-not-restated.md.
