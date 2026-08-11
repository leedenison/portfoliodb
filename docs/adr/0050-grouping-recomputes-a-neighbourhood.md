# Grouping recomputes a neighbourhood rather than repairing one

[0041](0041-server-owns-transaction-grouping.md) makes the server derive the
partition, and [0097](../issues/0097-server-side-transaction-grouping.md) runs it
after every import over the whole of a user's stored data. A partition of
everything per upload is not wanted, so the engine works over a region. How it
works over that region is the decision here, and there are two shapes.

The first is to repair: find the groups that look wrong, let a rule take a
posting from a group a weaker rule assembled, and cascade -- the legs left behind
have lost the justification for their own claims, so they re-enter the working
set and the rules run again over what was freed.

**The engine recomputes. It loads a neighbourhood whole, partitions all of it
from scratch in one traversal, and writes only where its answer differs from what
is stored.** Nothing is disturbed, because nothing is preserved.

## The neighbourhood is a closure, not a window

A time window is the obvious reading and it is the wrong one. A `SCOPE_USER`
correlation ([0049](0049-a-human-assertion-is-a-correlation.md)) links a posting
to one years away, and no window contains that.

The neighbourhood is the **fixpoint of the rules' own candidate relations**: start
from the seed, ask every rule what it could link each posting to, add what comes
back, and repeat until nothing new arrives. Each question is an indexed lookup
rather than a scan -- token equality through `idx_tx_correlations_token`, the
trade rules through a range on account and date, the deposit rule through a range
on account and ordinal -- so the closure costs a handful of queries per frontier
posting and fetches nothing external.

## Why this shape and not repair

**Termination, and nothing else comes close.** The closure only ever adds
postings and is bounded by what the user has, so it reaches a fixpoint in at most
as many steps as there are postings. That is a well-founded measure, written
down, checkable.

The cascade has no such measure, and the reason is not obvious. It looks like one
exists: a rule may only claim a posting a weaker rule placed, so a posting's
precedence only rises, and rises are bounded. But a posting freed from a
disturbed group must *drop* to the floor -- it is no longer held by the rule that
placed it -- so levels move in both directions and the measure evaporates. Worse,
freeing does not only remove candidates, it adds them: a posting at the floor is
claimable by every rule above it, so two postings freed by unrelated
disturbances can pair with each other in a way neither could before. "It would
have claimed that the first time round" does not close the argument.

That may still terminate. It probably does, quickly, on real data. But
[0047](0047-grouping-runs-as-precedence-ordered-passes.md) turned down
group-then-narrow-then-regroup for precisely this -- "the third step has no
terminating argument" -- and it would be strange to accept here what was refused
there.

**The two have the same worst case, so this is not an efficiency argument.** A
runaway cascade and a large closure are the same object: a big connected
component in the candidate graph. One computes it by monotone accumulation before
touching anything; the other discovers it while interleaving reads, writes and
re-claims. The difference is the proof, not the cost.

## What rules must therefore provide

Every rule states, for a given posting, what it could link that posting to, as a
**bounded indexed query**. This is what seeds the closure, and it is an
admissibility test: a rule whose candidates can only be found by scanning cannot
be admitted, because nothing could compute the region it needs.

The reach is the *whole* predicate, not its range component. Closing over "within
an ordinal span" alone would walk an entire account's history, since consecutive
references chain without end; closing over "same account, equal to the penny, and
a directed gap within the span" does not, because the chain would need a chain of
equal amounts to run along.

## Consequences

**A rule never needs to disturb its own level.** The case that seemed to require
it -- a leg paired on a wide-but-passing gap, whose true counterpart arrives in a
later upload -- resolves itself, because both candidates are in the neighbourhood
together and 0047's global ranking within a pass picks the better one. Correction
falls out of ranking rather than needing a mechanism, so 0047's "claims are
irrevocable" holds within a run and needs no exception across runs.

**The worst case is a full partition of one user, and that is correct.** If the
closure reaches everything, the engine has done what 0041 describes and is merely
slow. It reads stored data and fetches nothing, which is what licenses widening
freely. The neighbourhood buys away the per-import cost; it is not what makes the
answer right.

**A stored per-posting precedence is about the boundary.** Recording which rule
placed a posting is still worth doing, but not to gate what may disturb it. It
tells the closure whether a posting at the edge was placed by something
authoritative enough to stop at, or is floor-level scaffolding to pull in and
redo.

**The seed needs two sources.** Groups carrying an unaccounted-for transfer or a
residual worse than `SOURCE_ROUNDING` find *fragments*, which is most of what
needs repairing, and `idx_txs_residual_postings` indexes exactly those. They
cannot find a converter's wrong pairing of two similar trades, which balances
with only a rounding residual and looks settled from outside. So the cadence
trigger seeds from residuals and the import trigger seeds from the postings that
arrived, whatever state their groups are in.
