---
status: closed
title: Explain a purchase's gap with the charges that account for it
milestone: M15
dependencies: [0111]
---

Attach a per-trade charge to an acquisition whose own stated figures account for it,
for the charges no converter could name.

## Motivation

0111 names the charges a fee schedule resolves. What is left is the ones it cannot:
a variable stamp duty or FX charge, and any bucket whose counts disagree.

For a purchase the source has already stated the answer twice. Fidelity writes
7390.19 for the trade and -7380.19 for the cash that settled it, and the 10.00
between them is the charge. That is a figure the source wrote rather than a
proximity, which is what makes it evidence of the occurrence rather than of the day.

## Design

`Charge` at precedence 400, below `Attaches`, so a pointer a source stated always
beats an amount the server inferred. Both run below the rules that decide the
partition, because both only add to one.

For each acquisition, the gap is its stated total less the cash that settled it
**less the charges its group already holds**. That last term is what stops the two
rules fighting: a buy's gap is typically a dealing fee plus a stamp duty, so once a
stated pointer has claimed the fee the whole gap is no longer a subset of what is
left, and without netting the rule explains nothing.

Then a subset of the bucket's free charges summing to the gap, capped at three,
smallest subset first. Candidates are ranked across the neighbourhood before any is
taken, since two purchases on one day can both be explained by a 7.50 fee.

`Expand` is `expandByDay`, which after 0109 buckets on the order date. No new access
path.

## Consequences

A disposal is not attempted. Fidelity reports gross proceeds, so its stated total
equals its cash in exactly and there is nothing to explain -- a fact about the source
rather than a limitation here.

`alice-fidelity` falls from 617 groups to 613, and `bob-ibkr` is unchanged at 28, its
22 charges having been grouped by their `FITID` all along.

The fixture yield is low, and the reason is the fixtures rather than the rules: they
were extracted before a posting carried two dates, so they state one instant in both
-- which is exactly what a charge and its trade disagree about in a real export. Run
over the same two exports regenerated with both dates, the two rules together absorb
**81 of 88 charges (92%)** and **78 of 78 (100%)**, against the 4 the flattened
fixture captures. So the goldens pin the rules against regression rather than
measuring what they do.

| | groups before | after | charges absorbed |
| --- | --- | --- | --- |
| Fidelity export A | 574 | 493 | 81 |
| Fidelity export B | 617 | 539 | 78 |
| Schwab export | 142 | 142 | 0 -- its charges were already grouped by their record's own correlation |

`Unbalanced` moves on none of them -- 78, 88 and 4 before and after -- which is the
check that attaching is balance-neutral: an `EXPENSE` posting's counter-leg is routed
into the same group. `Buys`, `Sells` and `Other` are likewise unmoved, so a trade
that gained its charges is still the same trade.
