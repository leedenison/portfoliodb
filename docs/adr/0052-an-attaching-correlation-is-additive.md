# An attaching correlation is additive

Some sources report a subordinate leg as a record of its own while saying which record
it belongs under. A Fidelity dealing fee is its own row with its own reference number;
a withheld tax is its own row beside the dividend it was taken from. The leg is not a
peer of the event -- it is part of it.

[0048](0048-correlations-declare-their-own-semantics.md)'s `MATCH_EXACT` is a peer
relation: two postings holding one token are saying the same thing about themselves,
and together they *make* a group. That is the wrong statement here.

**A correlation may declare `MATCH_ATTACHES`: the token names another posting's
identifier, and the bearer joins whatever group that posting is placed in. The engine
gains `State.Attach`, the only operation that can add to a group a rule already
formed.**

## Why not just share a token

Because `Exact` runs at precedence 1000 and claims every posting sharing a token.
Stamp a trade's reference onto its dealing fee and `Exact` claims `{asset leg, fee}`
first; `trade.Apply` skips postings that are already claimed, so the trade can never
pair with its cash leg and the cash row is left a singleton. The group becomes the
charge and the trade rather than the trade and the money that settled it.

Avoiding that would mean the converter stamping one token across the asset leg, the
cash leg and every charge -- which is converter-side grouping under a new name, and
restores exactly the pairing rules
[0041](0041-server-owns-transaction-grouping.md) moved to the server.

## The three properties

*Directed.* Only the bearer carries it, as with `MATCH_ACCOUNT`. The named posting
says nothing in return.

*A reference, not a shared key.* The token is somebody else's identity. So the pass
compares the bearer's token against other postings' tokens, and concludes only that
the bearer joins theirs.

*Additive, therefore subordinate.* It contributes no evidence about the posting it
names, so it cannot decide where that posting goes. That is why `Attaches` sits at
precedence 500, below every rule that decides a partition: it can only follow one.

Those properties are about the comparison, not about fees. The same value serves a
withheld tax pointing at its dividend and cash in lieu pointing at its corporate
action, so there is one rule rather than one per problem, and the broker-specific
judgement that produced the pointer stays in the converter that had it.

## `Attach` does not weaken irrevocability

`State.Claim` refuses whole any claim naming a posting that has gone, and cannot
express the distinction `Attach` names: which member is the anchor and which are the
postings contributed.

The anchor is read and never written. Its group, its resolution and its claimed state
come out unchanged, so nothing an earlier rule decided is disturbed and no posting
moves between groups. What is added was free, and no rule had decided anything about
it. A contributor another rule already claimed is refused rather than moved, and the
refusal is whole. This is 0048's "a later pass may add to the group but may not split
it", and it is the only way to do it.

## Ambiguity is declined rather than guessed

Several postings can carry one token: an OFX record stamps its `FITID` on every leg it
produced. While those are in one group there is no ambiguity -- the pointer names the
event, and any member names the same group.

When they are not, the pointer would be choosing between two events with no evidence
to do it, so the rule attaches nothing. With the default ordering this cannot arise,
because `Exact` runs first and puts a token's carriers together; the guard is what
keeps a per-broker reordering ([0047](0047-grouping-runs-as-precedence-ordered-passes.md))
from turning into a wrong grouping.

A pointer whose token nobody carries also attaches nothing. It names a posting outside
the neighbourhood or one the source got wrong, and inventing a group for it would
assert something the evidence does not.

## Consequences

`Attaches.Expand` reaches through `PostingsByToken`, the index `Exact` already uses,
so the rule states no reach without an index behind it
([0050](0050-grouping-recomputes-a-neighbourhood.md)) and adds none.

It reaches in one direction. A posting carrying a token does not thereby reach the
postings pointing at it: it says nothing about them, and a bare identifier would
otherwise drag in every reference near it.

`State.Group` is added so a rule can ask whether two postings are already together. It
is the only thing a rule may learn about the partition forming, and it is what tells
"three legs of one event" from "two postings that are not together".
