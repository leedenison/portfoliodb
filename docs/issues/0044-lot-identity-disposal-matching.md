---
status: open
title: Lot identity and disposal matching
---

Give acquisitions a lot identity and make disposals reference the lots they
reduce.

## Motivation

`unit_price` is recorded on each tx, so cost basis is derivable by replay, but
there is no lot identity and no disposal matching -- a sale does not reference
what it sold. Holdings are `SUM(quantity)` grouped by instrument, which
collapses every acquisition into a scalar.

What that scalar costs:

- **No realised gain.** Current value is computable; return is not.
- **No unrealised/realised split**, and no way to attribute a change in value
  to market movement versus contributions and withdrawals.
- **No capital gains reporting**, which needs acquisition history per
  disposal.

The information is present in the transaction data. It is discarded by the
aggregation.

## Inspiration

Beancount's inventory model, where a posting carries a cost
(`10 SYM0 {£4.50}`) and a disposal nominates the lot it reduces, so that the
gain falls out of the requirement that the transaction balances rather than
being computed separately.

## Design

- Acquisitions get a lot identity; disposals reference the lot or lots they
  reduce, with a quantity per reference where a disposal spans lots.
- Beancount derives inventory by replaying the journal rather than storing it.
  Deriving on every query is expensive here and storing risks drift, so the
  likely shape is stored lots plus a periodic re-derivation that asserts
  equality -- the same reconciliation pattern as 0043.
- Corporate actions must adjust lots, not just totals: `split_factor_at` and
  the `split_adjusted_*` recompute need a lot-aware equivalent.
- Composes with 0036: with grouped postings, the disposal and the resulting
  gain are legs of one balanced group and the gain is implied by the zero-sum
  rule.

This issue covers the data model only. The choice of matching method and the
gains computation are 0045.
