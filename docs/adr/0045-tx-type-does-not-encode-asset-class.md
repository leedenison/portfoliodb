# Transaction type does not encode asset class or direction

`TxType` is an OFX vocabulary, and OFX spells a trade as a product of three
factors: the direction, the asset class and the event. `BUYSTOCK`, `BUYOPT`,
`BUYMF`, `BUYDEBT`, `BUYFUTURE` and `BUYOTHER` are one event named six times.

**The type carries the event only.** Asset class becomes a stated
`asset_class_hint`, and direction stays where it already is.

Direction is the sign of `quantity`, and has been since
[0020](0020-double-entry-postings.md) made holdings a plain `SUM(quantity)` with
no type-based sign flip. Naming it again in the type is a second spelling that can
disagree with the first.

Asset class is a routing hint that instrument resolution overwrites.
[0013](0013-security-type-hint-vs-asset-class.md) already says so: the hint is
coarse, it is not authoritative, and the canonical class comes from the identifier
plugins. Deriving it from a twenty-three-arm switch over the type buys nothing a
field would not, and it makes the two factors inseparable -- a source cannot state
the asset class without also committing to a direction and an event, or state the
event without also committing to an asset class it may not know.

That inseparability is the real cost, and it is what
[0044](0044-tx-type-is-declared-and-resolved.md) needs removed. The declaration a
source makes should be as specific as that source can be about *each* thing it
knows, and a cartesian product forces it to be equally specific about all of them
or not at all.

## Consequences

The hint stops being derived and starts being stated, so `TxTypeToAssetClass`,
`TxTypeToInstrumentKind`, `AssetClassToTxTypeStrings` and `AssetClassToTxTypesMap`
go, along with the reverse-mapped `tx_type IN (...)` construction the ignore rules
build to delete by asset class. `IsAssetClassCompatible` survives unchanged in
substance but compares a stated hint against the resolved class rather than an
inferred one, which is what 0013 wanted of it.

0013 is amended rather than superseded. Its decision -- that the routing hint and
the canonical asset class are separate layers, and that the coarse guess must not
be mistaken for confirmed identity data -- is exactly right and is why the hint
survives at all. What changes is where the guess comes from.

`exchangeTypes` in the ingest balancer shrinks from fourteen hand-listed values to
a single predicate. It is the clearest illustration of the problem: it documents
itself as "the tx types whose counter-leg is money rather than the commodity the
posting is in", which is a statement about one factor, enumerated across every
combination of the other two, with nothing to catch a value someone forgets to add.
