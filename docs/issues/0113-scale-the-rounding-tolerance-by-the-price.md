---
status: closed
title: Scale the rounding tolerance by the precision of the price
milestone: M15
---

Add the price's own rounding to the tolerance that separates `SOURCE_ROUNDING` from
`IMBALANCE`, so a residual that is only the printed price failing to reproduce the
cash figure stops reading as a missing leg.

## Motivation

A group balances on weight, and a security leg weighs at `quantity * unit_price`. A
price printed to 2dp is out by up to half a penny per unit, so a large position is out
by pounds:

```
INRG   2676 units, price printed 7.67, cash in 20514.62
       20514.62 / 2676 = 7.66615      printed as 7.67
       x 2676 units                   = 10.30      routed to IMBALANCE
```

The tolerance was a fixed 0.005, which covers only the rounding in the cash figure. So
every large trade reported as an imbalance: 70 of 70 currency imbalances in the sample
data, which was the whole of one broker's report.

`residual.go`, `docs/spec/postings.md` and
adr/0026-exact-decimals-bounded-by-closure.md all named this and deferred it. 0026
states it as a consequence already accepted -- "the tolerance can be inferred from the
scale of the amounts rather than fixed as a constant" -- so this is that deferred half
rather than a new decision.

## Design

```
tolerance = moneyTolerance + SUM over converted legs of |units| x half the last digit of the price
```

A leg counts only where its weight was derived by pricing it into the settlement
currency, which `weight <> quantity` says. A cash leg's price of 1 is exact by
definition, so a deposit run's tolerance is unchanged. Because
`weight = quantity * price * contract size`, the term is `|weight| * halfUlp / |price|`
and needs no join to `instruments` for the multiplier.

The precision is floored at `residual.PriceScaleFloor = 2` rather than read off the
figure. Fidelity strips trailing zeros in its own download -- the sample writes prices
at 0, 1 and 2 decimal places, with `47.1` and `47.11` in one instrument's series -- so
the stated scale understates what was quoted. Reading it off inflates the bound tenfold
on a 1dp price and a hundredfold on a 0dp one, and reclassifies exactly the same
groups: largest bound 338.91 as stored against 38.82 floored. A floor rather than a
fixed precision, so IBKR (6dp) and Schwab (4dp) keep their own.

## Evidence

Over the 70 currency imbalances in the dev database, `residual / bound` reaches 0.970
and never exceeds 1. The tightest is 32 units at 1067.67 leaving 0.16 against a bound
of `32 * 0.005 = 0.16` -- the price error saturating its theoretical maximum. Residuals
crowding a bound they never cross is what a correct error model looks like.

Applying the shipped SQL to that database reclassifies all 70, and leaves all 132
`TRANSFER_CLEARING` groups untouched.

## Consequences

The change is monotone: the bound is `moneyTolerance` plus a non-negative term, so
nothing can move from `SOURCE_ROUNDING` to `IMBALANCE`.

What it costs is that summing per-leg bounds is a worst case, assuming every price
erred the same way, so a small missing leg can hide inside a high-quantity trade's
bound -- a 30 pound discrepancy on a 6,778-unit position sits within a 34 pound bound.

The grouping engine's `Opts.Money` deliberately does **not** scale. It compares two
figures the source stated, where no price arithmetic has happened; the balancer
compares a derived weight against a stated figure. The spec previously tied the two
together and now says why they differ.

`SplitType` is untouched: a residual left by a period replace is the value of the legs
that went and can be any size.

The `exports_test.go` goldens do not move. Their `Unbalanced` count comes from an
independent oracle asking whether a group closes, not from the classifier, and they do
not inspect account types.
