# Security type hint is routing-only, distinct from canonical asset class

Amended by [0045](0045-tx-type-does-not-encode-asset-class.md): the hint is
stated on the posting rather than derived from the transaction type. Only where
the coarse value comes from changes.

A security type hint is passed to plugins for routing only. It shares the
asset-class vocabulary but is not authoritative: the canonical asset class
stored on an instrument is always determined by the identifier plugins during
resolution. Keeping the routing hint and the canonical asset class as separate
layers prevents the coarse guess from being mistaken for confirmed identity
data.

What makes the hint coarse is a property of the source rather than of the field.
A broker statement often cannot tell a share from an ETF, and an OFX file has no
ETF type at all, so a stock-like row says only that much. That coarseness is
carried as a value in the vocabulary -- the internal nodes of the tree in
[identifiers.md](../spec/identifiers.md), of which `EQUITY` is the one a
statement line usually reaches -- rather than by making a source pick a leaf and
then tolerating the mistake downstream. The two readings are not
interchangeable: a tolerance applied at a comparison is invisible at the site
that stated the value, so each comparison ends up choosing its own and the
copies drift apart, which is what issue
[0163](../issues/0163-overlapping-predicates-answer-one-question-with-different-tolerances.md)
found. A value says what the source could defend, once, where every reader can
see it.

The same applies to plugins, in both directions: a plugin declares the classes
it covers and answers with the specificity it actually has, so a provider that
got as far as "a derivative" says `DERIVATIVE`.

## Consequences

Nothing compares two asset classes for equality. A comparison is either strict
-- `EQUITY` does not corroborate `ETF`, having never been a claim about which of
the three -- or permissive, and neither default is right everywhere: deciding
whether to try a plugin is permissive, because excluding a row we know little
about loses the row while an extra lookup costs a call, and deciding whether a
source corroborates an answer is strict. `server/assetclass` exposes `MustBe`
and `MayBe` and no equality, so every call site says which it means, as
[0044](0044-tx-type-is-declared-and-resolved.md) has transaction types do.
