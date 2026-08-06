# A split restates only the contracts listed on its effective date

OCC adjusts an option contract on a split's effective date: from the open that
day it lists under a new symbol and strike. A contract that had already expired
was not listed to adjust, so it was never restated and its final symbol and
strike are the ones it traded under. Restatement is therefore bounded at the
option's expiry, and the bound is inclusive -- a contract expiring on the ex_date
is listed and exercisable that day, so it is restated like any other.

The bound belongs in both places that carry an OCC symbol forward:

- `ListPendingOptionSplits` selects `s.ex_date <= o.expiry`, so the retroactive
  pass never rewrites a contract the split did not reach. Without it, an option
  expiring before the ex_date satisfies the `identity_as_of < ex_date` guard and
  is rewritten to a contract that never traded.
- `AdjustOCCForKnownSplits` rebases an OCC hint only as far as `min(now,
  expiry)`, so an expired option's "current" symbol is the one it expired under.

## They have to move together

Fixing only the query would have been worse than fixing neither. The rebased hint
is what `ResolveByHintsDBOnly` looks up, so a broker import of an expired option
would search for a post-split symbol, miss the stored pre-split row the pass now
correctly leaves alone, and `EnsureInstrument` would create a second instrument
for the same contract. Today the two agree only because the pass rewrites the row
to match the over-rebased hint -- agreeing on the wrong answer.

## Consequences

- The `OCC_AT_EXPIRY` internal hint is retired. It existed to send OpenFIGI the
  symbol an expired contract wore at expiry while the ordinary OCC hint ran on to
  today; with rebasing bounded at expiry the two are the same value, and the
  machinery that carried it -- the resolver skip clauses, the OpenFIGI id-type
  mapping, and the block that overwrote a returned OCC with the rebased one --
  goes with it.
- An expired option's identity is final. Only splits effective during its life
  can make it pending, which is what `identity_as_of` versus `ex_date` (see
  [0017](0017-option-identity-reflects-ex-date.md)) already decides.
- 0017 says a rebased hint stamps `now()` "when every OCC hint was rebased onto
  today". Read against this bound that is "as far forward as it can go" -- today
  for a listed contract, expiry for an expired one. An expired contract will not
  be restated again, so its expiry *is* the most current its identity gets. The
  distinction is unobservable in any case: the pending-split query requires
  `ex_date <= expiry`, so a stamp of either expiry or `now()` sits on or after
  every ex_date that could select the option.
- Nothing changes for a live option: `min(now, expiry)` is `now`, and the pass
  selects it exactly as before.

0017 decides which clock the guard reads. This decides which options it reaches.
