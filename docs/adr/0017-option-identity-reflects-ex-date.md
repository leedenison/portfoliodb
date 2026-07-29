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

`identity_as_of` moves only when the identity is genuinely re-derived: a plugin
identification, or a retroactive adjustment. It is deliberately **not** touched
by `EnsureInstrument`, which fires on every incidental match -- the
broker-description-only fallback, price import, instrument import. Bumping it
there is what allowed an unrelated import to disarm the guard permanently
(see 0055). It is carried on instrument export and restored on import, so a round
trip cannot make an already-adjusted option look unadjusted.

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
