---
status: open
title: Compute holdings in one share count rather than summing raw quantities
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

## Design

Decide what the raw column on `Holding` is for. There are two readings and they
lead to different work:

- It is a *diagnostic* -- what the source actually said, before adjustment. Then
  it is not a quantity that may be summed at all, and the fix is to stop
  aggregating it: report it per basis, or drop it from the API and the table.
- It is a *position in as-traded terms*. Then it needs a stated basis like a
  declaration has, and `holding_qty_in_basis` computes it.

The closed-position test is not ambiguous either way: it should read the
split-adjusted sum, where every row is already in one denomination. `qty_is_zero`
is exact since 0042, but the adjusted columns carry the declared rounding scale
that ADR 0028 explains, so the test needs the same kind of bound the assertion
comparison uses rather than an equality against zero.

`ListResidualBalances` sums raw `quantity` the same way. Money is unaffected, but
a residual left by an unpaired `JRNLSEC` is a quantity of shares and is exposed
to the same error.
