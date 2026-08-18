---
status: superseded by ADR-0055
---

# Option split adjustment keys off ex_date, not knowledge time

When a split lands on an option's underlying, the option's OCC symbol and strike
need retroactive adjustment -- unless the stored identity already reflects that
split. The guard originally compared `instruments.identified_at` against
`stock_splits.first_known_at`: if we had identified the option after we learned
of the split, the identity was assumed already correct.

That is the wrong clock. OCC adjusts a contract on the split's **effective**
date: until the ex_date the contract lists under its original symbol and strike,
and from the open that day under the adjusted ones. Identifier plugins report
what is currently listed, so a lookup on 1 March for a 1 June split returns the
**pre-split** OCC however long the split has been public. Comparing knowledge
times marks that identity already-correct and skips an adjustment it needs.

The guard therefore compares the identity against `ex_date`, and the column it
reads is `instruments.identity_as_of` -- the point in market time the stored
identity reflects. This also puts the guard on the same clock as
`AdjustOCCForKnownSplits`, which already filters purely on `ex_date` and ignores
`first_known_at` when re-basing an OCC hint during resolution.

## The caller stamps, not the storage layer

`EnsureInstrument` is find-or-create by identifier. It cannot know where the
identifiers came from -- a plugin lookup of current market data, a broker CSV
description, a price file exported months ago -- and `identity_as_of` is a claim
about exactly that provenance. So it never writes the column, on create or on
match, and each caller stamps what it actually knows:

| Caller | Stamps | Because |
| --- | --- | --- |
| Plugin resolution (`ResolveWithPlugins`, winner path) | the vintage of the OCC hint it was given, else `now()` | The plugin answers the question it was asked -- see below. |
| `ApplyOptionSplit` | `now()` | The identity has just been re-derived against a split effective today. |
| Price import fallback (`ensureWithSuppliedIdentifier`) | the request's `exported_at`, else `now()` | The OCC is stored exactly as supplied, so it reflects the market as of the file's vintage. |
| `ImportInstruments` | the payload's `identity_as_of`, if present | The exported value is the only evidence of vintage available. |
| Broker-description-only fallbacks | nothing | They store no market-derived identifier at all. |

Bumping the column on an incidental match is what allowed an unrelated import to
disarm the guard permanently (see 0055). Leaving it NULL on a create whose
identity *did* come from somewhere is the opposite error: the pass would then
re-apply splits already baked into the stored symbol. Neither default is safe in
general, which is why the decision sits with the caller.

## A plugin answer is only as current as the question

A plugin lookup does not by itself make the resulting identity current. An OCC
lookup is identity **by value**: the provider answers about the contract it was
named, not the contract the caller meant. That name comes from a broker file at
the transaction's vintage, and `AdjustOCCForKnownSplits` is what carries it
forward to today -- but only across splits already stored. A split we have not
yet learned of leaves the hint at its original vintage, the provider answers
about the pre-split contract under the pre-split symbol, and the identity we
store is that of a contract as it stood before the split.

So `AdjustOCCForKnownSplits` reports the market time its returned hints reflect,
and the winner path stamps that rather than `now()`: `now()` when every OCC hint
was rebased onto today (or there was no OCC hint at all), and the hint's own
vintage when one was left alone. Stamping `now()` unconditionally was the
original form of this decision, and it silently disarmed the guard for exactly
the case the retroactive pass exists to cover -- historical broker transactions
imported before the underlying's splits are known, which is the ordinary
ordering for a new user. The identity looked derived after the ex_date, so the
option was never adjusted and kept a symbol and strike that no longer described
any listed contract.

This is the same rule as the price-import row of the table above, and for the
same reason: when the stored OCC is the one that was supplied, the identity
carries the supplier's vintage. That a plugin was consulted in between does not
change it.

The column only ever moves **forward**. A caller supplying a vintage cannot tell
whether `EnsureInstrument` created the row or matched an existing one, so
`SetIdentityAsOf` ignores a value older than the stored one -- otherwise a stale
price file could drag the stamp backwards and re-expose an already-adjusted
option to the pass.

`identity_as_of` is carried on instrument export and restored on import, so a
round trip cannot make an already-adjusted option look unadjusted.

## Accepted gaps

An instrument import whose payload carries no `identity_as_of` -- a hand-written
file, or an export predating the field -- leaves the column NULL on a newly
created row, so an option in it would be adjusted for splits its OCC may already
reflect. Defaulting to `now()` instead would stamp on match as well as on create,
which is the churn this ADR exists to remove, so the NULL is accepted. Supplying
`identity_as_of` in the payload is the fix for anyone who hits it.

Broker-description-only instruments never carry an OCC identifier and have no
asset class, so `ListOptionsByUnderlying` does not return them and the NULL is
inert.

## Consequences

- `stock_splits.first_known_at` is no longer read for option adjustment. It
  remains for corporate-event export and import knowledge preservation
  (see 0051).
- The guard is now stable under knowledge revision. `first_known_at` can move --
  `DeleteStockSplit` followed by a re-upsert resets it to `now()`, which used to
  un-guard every already-adjusted option and adjust it a second time. `ex_date`
  does not move.
- A NULL `identity_as_of` means the identity predates every split, so it is
  adjusted. That is the safe default for an instrument created by any path that
  did not derive its identity from market data.
