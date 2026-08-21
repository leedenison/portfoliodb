# Transaction type is declared as a set and resolved by grouping

[0041](0041-server-owns-transaction-grouping.md) makes the server decide which
postings are legs of one event. That leaves a converter answering a question it is no
longer in a position to answer: what a row *is*, when its broker will not say.
Fidelity reports a switch's sale proceeds and money arriving from another account
through the same `Cash In`, and the converter told them apart by whether the row
happened to pair with a trade -- a grouping decision. A single-valued type gives the
converter no way to hand that decision over, so this is a prerequisite of 0041 rather
than an improvement alongside it.

**A posting carries `broker_tx_type`, a set of candidate types, and
`resolved_tx_type`, the single value grouping narrowed it to.** The first is what the
source said, at whatever specificity it manages; it is an input. The second is
derived, and is what everything downstream reads.

## A set, named by a hierarchy

The types form a tree, so a source asserts the most specific node it can defend and
an ancestor means "somewhere in this subtree". OFX's bare `INCOME` is honestly income
of an unstated kind, and `INVEXPENSE` an expense of an unstated kind; both are
internal nodes, and both are legal values rather than degraded ones.

A tree alone is not enough. Fidelity's `Cash In` is either a trade's cash leg or a
transfer, which are not siblings, so their only common ancestor is the root. A strict
tree would collapse that to "unknown" and discard the useful fact that the row is
definitely not a dividend and definitely not a fee. Shaping the tree around the
ambiguities brokers happen to exhibit would recover the case at the cost of trading
one broker's arbitrary vocabulary for another's.

So the field is a **set**, and the hierarchy names the sets worth naming. A set of
candidates is a node in the powerset lattice; a tree is a sublattice given names. A
singleton is a fully specific assertion, an internal node is shorthand for its leaves,
and an arbitrary set is an explicit ambiguity that no tree could reach.

## What a less specific declaration means

**A rule fires only if it holds for every candidate.** A row that may be a transfer
and may be a trade's cash leg is not treated as a transfer, because it is not one
under every reading.

**Grouping resolves the set**, and
[0047](0047-grouping-runs-as-precedence-ordered-passes.md) makes that fall out of
which pass claims the row rather than needing a phase of its own. Where nothing
narrows it, `resolved_tx_type` takes the **common ancestor** of the surviving
candidates -- `INCOME` for `{DIVIDEND, INTEREST}`, and the root for a cross-branch
set. One rule, and it keeps whatever was known rather than discarding it.

What a set may contain is bounded by
[0046](0046-declared-ambiguity-is-bounded-by-weight-neutrality.md).

## Consequences

`resolved_tx_type` can hold an internal node, so a query for a leaf is a subtree test
rather than an equality. The expansion is computed where the vocabulary lives and
passed as `tx_type = ANY($1)`, so no SQL learns about the hierarchy and the existing
indexes keep working -- the pattern the ignore rules already use to expand an asset
class into the types that imply it.

The two expansions differ -- *may be* includes the node's ancestors, *must be* does
not -- and either default is silently wrong, one dropping rows from a report and the
other double-counting. So the vocabulary exposes `mustBe` and `mayBe` and no bare
equality at all, forcing every call site to state which question it is asking.

The value meaning "ambiguity survived" is an explicit member of the vocabulary,
distinct from `TX_TYPE_UNSPECIFIED`: `broker_tx_type` rejects "the field was not set"
while `resolved_tx_type` has to be able to say "the source was not specific enough
and grouping could not settle it"
([0038](0038-controlled-vocabularies-are-shared.md)).

Declared and derived have to be stored separately. The archive carries
`broker_tx_type` and not `resolved_tx_type`
([0043](0043-grouping-does-not-travel-in-the-archive.md)).

## Considered: a single value plus an ambiguity field

Keeping one type and adding a second field naming what it might otherwise have been
expresses the same thing as a set with worse ergonomics: two fields to read together,
a privileged member with no claim to privilege, and no canonical spelling for the
three-way case.
