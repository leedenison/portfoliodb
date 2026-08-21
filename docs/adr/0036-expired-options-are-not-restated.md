# A split restates only the contracts listed on its effective date

OCC adjusts an option contract on a split's effective date: from the open that day
it lists under a new symbol and strike. A contract that had already expired was not
listed to adjust, so its final symbol and strike are the ones it traded under.
Restatement is bounded at the option's expiry, and the bound is inclusive -- a
contract expiring on the ex_date is listed and exercisable that day, so it is
restated like any other.

`ListPendingOptionSplits` selects `s.ex_date <= o.expiry`, so the retroactive pass
never rewrites a contract the split did not reach. Without the bound, an option
expiring before the ex_date satisfies the identity guard and is rewritten to a
contract that never traded.

## The bound has to hold everywhere a name is carried forward

The pass is not the only carrier, and fixing one carrier without the other is worse
than fixing neither: the two then agree on the wrong answer. While an OCC hint was
rebased across known splits before lookup, the same `min(now, expiry)` bound had to
apply there too, or a broker import of an expired option would search for a
post-split symbol, miss the stored pre-split row the pass correctly left alone, and
create a second instrument for the same contract.

Rebasing is retired. [0055](0055-identifier-validity-is-an-interval.md) makes a
split mint a name rather than rewrite one, so a hint of any vintage matches a stored
row by value and there is nothing left to rebase. The requirement survives the
retirement rather than being answered by it: the pair that has to agree is now the
query and the stored interval. A pass that restated an expired option would close
the name the contract expired under, and a file stating that name would have nothing
left to resolve to.

## Consequences

- The `OCC_AT_EXPIRY` internal hint is retired, along with the resolver skip
  clauses, the OpenFIGI id-type mapping and the block that overwrote a returned OCC
  with the rebased one. It existed to send OpenFIGI the symbol an expired contract
  wore at expiry while the ordinary OCC hint ran on to today; with rebasing bounded
  at expiry the two are the same value.
- An expired option's identity is final. Only splits effective during its life can
  make it pending.
- Nothing changes for a live option: `min(now, expiry)` is `now`.

[0017](0017-option-identity-reflects-ex-date.md) decides which clock the guard
reads. This decides which options it reaches.
