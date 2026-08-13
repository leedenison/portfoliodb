---
status: open
title: Declare a cost basis alongside a pad
milestone: M19
dependencies: [0044]
---

Let a holding declaration state what the declared position cost, so that the lot
a pad creates has a basis instead of an unknown one.

## Motivation

A pad is a declared quantity with no cost -- that is what makes it a pad. Under
0044 the lot it creates therefore carries a NULL basis, and every disposal that
draws on it reports that the gain could not be calculated. For a user whose
transaction history does not reach back to inception, that is most of their
oldest holdings, which are also the ones with the largest gains.

0044 deliberately refuses to invent a basis
(adr/0031-lots-are-derived-and-unknown-basis-is-a-value.md). This is the
declaration path it refuses in favour of: the user knows what they paid, and
`holding_declarations` is already the place they say what they know.

docs/spec/fixed-point.md has listed this as a future extension since the
declaration model was written.

## Design

`holding_declarations` gains a nullable `declared_cost NUMERIC` and a
`cost_currency TEXT`. Four properties settle the rest.

**A total, not a per-share price.** Total cost is split-invariant -- the
`quantity * unit_price == split_adjusted_quantity * split_adjusted_unit_price`
identity in docs/spec/corporate-events.md is exactly that -- so a declared total
needs no `share_count_basis` of its own and is never restated when a split lands.
A per-share price would need one, and deriving a total from it divides
(adr/0026-exact-decimals-bounded-by-closure.md).

**The declared cost is the cost of the declared position; the pad lot takes the
remainder.** This mirrors `init_qty = declared_qty - running_balance`: the pad
lot's cost is `declared_cost` less the cost of the lots the real transactions
leave open at `as_of_date`. A subtraction, so it stays exact, and the
recalculation triggers in docs/spec/fixed-point.md apply unchanged.

Where that goes negative the declaration disagrees with the imported history --
the real transactions already account for more cost than the user says the
position cost. That is a reportable problem rather than a basis: the pad lot's
cost stays NULL and the mismatch is surfaced, in the manner of the assertion
check in 0043.

**Only the pad carries one.** An assertion generates nothing, so a cost declared
on one has nothing to attach to. A *checked* cost assertion is the obvious
symmetry and is deliberately out of scope; it wants the disposal history to be
trustworthy first.

**The INITIALIZE group does not change.** The pad posting and its `EQUITY`
counterparty go on weighing in shares. Putting the cost into them would need the
pad to weigh in currency, which is the shape
adr/0024-group-balance-is-checked-on-weight.md rejected for transfer legs and for
the same reason. The declared cost is an input to the lot derivation, exactly as a
real acquisition's cost is read from its group.

## Plumbing

The declaration API, recalc and UI already exist in
server/service/api/declarations.go, server/service/api/recalc.go and the Opening
Balances tab. The two fields carry through all three. The form needs to be clear
that it is asking for the total paid for the whole declared position, including
any dealing costs, and that leaving it blank is allowed and means the gains
reporting will say the basis is unknown.

## Note

This does not make a padded portfolio's gains authoritative. A declared cost is a
user's recollection, and 0045 reports an estimate of taxable gain rather than a
liability in any case. What it removes is the case where a gain cannot be reported
at all.
