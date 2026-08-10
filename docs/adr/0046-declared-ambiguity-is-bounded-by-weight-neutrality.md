# Declared ambiguity is bounded by weight neutrality

[0044](0044-tx-type-is-declared-and-resolved.md) lets a source declare a set of
candidate types. [0024](0024-group-balance-is-checked-on-weight.md) makes the type
decide how a posting weighs: it "says what units the other side of the event is
expected in", and a leg whose counter-leg is money converts at its price while
everything else weighs its own quantity in its own commodity. A set can therefore
name candidates that weigh differently, and
[0029](0029-posting-weight-is-stored.md) stores the weight against a deferred
constraint that has no way to hold a maybe.

**A `broker_tx_type` set is admissible only if every member yields the same weight
in the same commodity for that posting's own quantity, price and currencies.**
Checked per posting at ingest, against the same rule the balancer applies.

The disagreement is worse than it first looks. `weightOf` returns a commodity as
well as an amount, so a priced security row that is either a trade leg or a
transfer weighs in the settlement currency under one reading and in the security
itself under the other. Balance is summed per commodity, so one reading balances
the group and the other leaves two residuals in it.

The check is cheap because the weight rule asks about the price before it asks
about the type: a posting with no price cannot convert at all, whatever it is. An
unpriced transfer-in is therefore weight-neutral under both readings and stays
expressible, and only a *priced* ambiguous security row is rejected -- which is the
case where the source has already answered the question, because a price is what a
trade has and a transfer does not.

## Consequences

Weight is a function of the posting's own fields alone: quantity, price,
currencies, contract size and `broker_tx_type`. It does not depend on the group, so
regrouping never rewrites it, and 0029's stored value stays fixed at the fact's own
time in the same way `split_adjusted_*` does. That is what keeps a regroup from
cascading into the balance constraint.

The invariant is a property of a posting, not of the vocabulary, so nothing in the
type hierarchy has to be restricted to enforce it.

## Considered: weighing an ambiguous posting under the every-candidate rule

[0044](0044-tx-type-is-declared-and-resolved.md)'s rule -- a rule fires only if it
holds for every candidate -- has a well-defined answer here: convert only if every
candidate converts, so an ambiguous row weighs in its own commodity. It is also the
safe direction by 0024's own asymmetry, since failing to convert leaves a visible,
attributable residual while converting wrongly deletes a holding and puts cash in
its place silently.

It was rejected because the weight is then wrong as soon as the row resolves to the
converting candidate, so resolution has to rewrite it -- and with it the group's
balance and any residual routed against that balance. Every regroup would then
cascade through stored weights into the deferred constraint, and 0029's argument
for storing weight rather than re-deriving it is that a posting's weight is fixed
when the posting is written.

## Considered: forbidding sets that span the converting boundary

Stating in the vocabulary that no set may contain both a converting and a
non-converting type is simpler to describe and needs no per-posting check. It was
rejected as both too strong and too weak: too strong because the same set is
perfectly admissible on an unpriced posting, and too weak because the boundary is a
property of the weight rule, which has guards -- the settlement-currency guard, the
cross-currency case -- that a vocabulary-level rule cannot see.
