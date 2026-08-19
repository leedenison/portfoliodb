---
status: closed
title: Compute holdings in one share count rather than summing raw quantities
milestone: M10
dependencies: [0043]
---

`ComputeHoldings` decides whether a position is closed, and reports a second
quantity, by summing raw `quantity` across postings that are not in the same
share count.

## Motivation

Both holdings queries in server/db/postgres/holdings.go select

```sql
SUM(t.quantity) AS quantity,
SUM(t.split_adjusted_quantity) AS split_adjusted_quantity
...
HAVING NOT qty_is_zero(SUM(t.quantity))
```

The second sum is fine: every row is already converted to today's share count.
The first is not. `quantity` is denominated in the row's own
`share_count_basis`, so a buy recorded before a split and a sell recorded after
it are in different units, and adding them scales the result by some part of the
split factor. This is the arithmetic docs/spec/bitemporality.md rule 4 forbids.

Two things read that sum:

- The `HAVING` clause, which decides whether the holding appears at all. A
  position that is genuinely closed can sum to non-zero and show up as a
  phantom holding; one that is genuinely open can sum to zero and vanish. Buying
  100 pre-split and selling the resulting 200 post-split nets to -100, not zero.
- `Holding.quantity` on the wire, which the holdings table renders as
  `(raw: N)` beneath the adjusted figure, so the number is user-visible.

0043 settled this for holding declarations and left the primitive behind:
`holding_qty_in_basis` converts each posting from its own basis into a stated
one, grouping by basis so the division happens once per denomination, and
returns the counts needed to bound its own rounding.

## Resolution

The raw column was settled as a diagnostic and dropped from the API. Rule 7 of
bitemporality.md already fixes the holdings API's denomination as today's share
count, so `split_adjusted_quantity` *is* the position and a second aggregate
beside it is either a restatement of the same number or a figure in no share
count at all. `Holding.quantity` is gone, the two queries no longer select
`SUM(t.quantity)`, and the holdings table renders one figure. Nothing about
per-row raw quantity changed: `txs.quantity` and its `share_count_basis` are what
the recompute pass and `holding_qty_in_basis` read, and the transaction list
still shows them where they mean something.

`qty_is_zero` took a second argument rather than being replaced. It now tests a
sum of `split_adjusted_quantity` against zero to within one unit in the last
place per contributing posting that may have rounded. Callers count those as the
postings whose adjusted quantity differs from their raw one: a row that converted
by 1/1 cannot have rounded, and counting the rest overstates only by the ones
that converted exactly, which is the safe direction for a bound. When no split
falls in the window the count is zero and the test is exact. The NULL branch
stayed as it was.

The tighter per-basis count `holding_qty_in_basis` returns was not used. It
bounds a sum computed from the raw column once per denomination, not a sum of
values already rounded per row, and two of the three queries could not call that
function anyway -- it is keyed by user and hardcodes `account_type = 'USER'`.

`ListResidualBalances` and `CountResidualBalances` share one aggregate and were
fixed with it. Money is unaffected because currency instruments never split, so
only share residuals move, and they now report in today's share count.

`GetUserValuation` was folded in although this issue did not name it: its day
grid gated on `NOT qty_is_zero(dh.qty)` over a cumulative split-adjusted sum,
which is the same equality against zero on a rounded number. The per-day count of
inexact postings accumulates over the same window as the position it bounds, and
`daily_holdings` became a lateral join so one lookup returns both.
