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
| Plugin resolution (`ResolveWithPlugins`, winner path) | `now()` | The plugin just read current market data. |
| `ApplyOptionSplit` | `now()` | The identity has just been re-derived against a split effective today. |
| Price import fallback (`ensureWithSuppliedIdentifier`) | the request's `exported_at`, else `now()` | The OCC is stored exactly as supplied, so it reflects the market as of the file's vintage. |
| `ImportInstruments` | the payload's `identity_as_of`, if present | The exported value is the only evidence of vintage available. |
| Broker-description-only fallbacks | nothing | They store no market-derived identifier at all. |

Bumping the column on an incidental match is what allowed an unrelated import to
disarm the guard permanently (see 0055). Leaving it NULL on a create whose
identity *did* come from somewhere is the opposite error: the pass would then
re-apply splits already baked into the stored symbol. Neither default is safe in
general, which is why the decision sits with the caller.

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
