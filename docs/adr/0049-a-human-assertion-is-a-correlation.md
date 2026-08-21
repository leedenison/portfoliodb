# A human assertion is a correlation, not an anchor

[0041](0041-server-owns-transaction-grouping.md) and
[0037](0037-transfer-matches-are-links-not-postings.md) leave a question open: group
ids churn when the partition is recomputed, so a grouping or a match a person asserted
by hand cannot be keyed on something that evaporates. Both conclude that human
judgement has to become an input replayed on every run, keyed on something that
outlives the groups it names, and both record that what that key is remains open,
because a posting has no natural key ([0002](0002-transaction-ingestion-model.md)).

**A person's assertion is recorded as a correlation on the postings it names, and
there is no key.** The asserter synthesises a token, stamps it on each member with
`SCOPE_USER` and `MATCH_EXACT`, and the engine's highest-precedence pass
([0047](0047-grouping-runs-as-precedence-ordered-passes.md)) claims them together like
any other exact match.

The open question assumed the assertion would live beside the postings and have to
find them again. It does not: a correlation is a field of a posting
([0048](0048-correlations-declare-their-own-semantics.md)), so it is already attached
to what it names. There is nothing to resolve, nothing to re-resolve after a regroup,
and no case where the thing a stored assertion points at cannot be found.

So one evidence shape serves a source and a person alike. The engine has one kind of
evidence and no special case, and 0048's transcription contract is unaffected: it
binds converters, which must not manufacture evidence the source did not supply, and
says nothing about a person deliberately supplying some.

## The same shape serves a merge and a pairing

What decides whether the engine merges the named postings or pairs their groups is the
postings' own type, not anything the assertion says. Where every named posting must be
a transfer the grouping engine declines the claim and the matcher links the two
groups, which preserves the account boundary
[0022](0022-typed-per-account-cash-flow-boundary.md) depends on and is the whole of
what [0037](0037-transfer-matches-are-links-not-postings.md) was protecting. Where
none is, the assertion merges. A mixed set is refused when it is asserted.

A `MANUAL` transfer match therefore is not a separate object keyed on group ids. That
reading does not survive contact with the code either: `applyChanges` drops every
`transfer_matches` row naming a group a posting left or the engine created, and nothing
rebuilds a hand-made one, so an id-keyed link is destroyed by any genuine repartition
of either side.

## An assertion lives and dies with the postings it is written on

Which is the whole of its lifecycle, and each half is what is wanted.

**A re-upload destroys it.** Ingestion is idempotent by replacement
([0002](0002-transaction-ingestion-model.md)), so the postings in the period go and the
correlations on them go too. The user is warned before the replace runs, and is not
offered a rebuild: a rebuild would have to decide whether a row in the new statement is
the row the person looked at, which is undecidable exactly when it matters -- the case
where the broker restated the period is the case where the answer might no longer hold.

**A change to any member destroys the whole assertion.** A replace is scoped to one
broker and one period while an assertion is most often made precisely across those
boundaries, so partial death is the ordinary case. Two guarantees settle it, and
deliberately no more:

1. A person is warned before an import commits that would modify a posting carrying a
   user-asserted correlation.
2. When such a posting is modified, that correlation and every other user-asserted
   correlation sharing its token are deleted.

The person re-asserts. Everything else -- replaying the survivors, flagging them,
deciding whether a two-of-three fragment still means what it meant -- needs the
assertion to record how many postings it named and then needs a judgement about a set
nobody asserted. This needs no arity, because deletion is by token and the token index
already finds the siblings.

**An archive round trip preserves it**, and survives on ordering. The replace collects
the tokens carried by the postings it is about to delete, deletes the postings, deletes
the correlations surviving elsewhere under those tokens, and only then inserts the
incoming postings with their own. The deletion therefore reaches what pre-dated the
write and nothing the write supplied. That is consistent with
[0043](0043-grouping-does-not-travel-in-the-archive.md), which drops the partition and
keeps the evidence.

## Considered: a content fingerprint over the posting's transcribed fields

The obvious reading of "keyed on something that outlives the groups it names": digest
the fields a re-upload reproduces -- broker, account, timestamp, amounts, declared
type, correlation tokens -- store assertions against that digest, and replay them on
every run. It buys the one thing the decision above gives up, which is an assertion
surviving a re-upload of its own period.

Rejected because it pays for that with a resolution step that has no good answer when
it fails. A digest either finds its postings or does not, and "does not" is ambiguous
between the row having genuinely gone and the broker having restated it in a way the
digest did not survive -- a corrected amount, a settlement date moved by a day.
Distinguishing those needs a fuzzier match, and a fuzzy match on a person's assertion
re-applies their judgement to rows they never saw. Nor is the digest cheap: identical
rows are legal ([0002](0002-transaction-ingestion-model.md) enforces no uniqueness), so
it needs an occurrence index beside it, which reintroduces exactly the positional
fragility it was meant to remove.

The correlation carries no such ambiguity, because it is not a reference. It is
present, or the posting is not.

## Consequences

**A manual grouping is protected by precedence, not by leaving it alone.** An
exact-token claim is a must-link
([0048](0048-correlations-declare-their-own-semantics.md)), and the pass that makes it
runs first, so a later pass can add to an asserted group but can never take a member
out of it. In particular there is no rule sparing a group the engine finds already
settled: such a rule would hold whether or not a person had said anything, would stop
the engine correcting a partition that is wrong but balances, and would protect the
wrong groups, since an assertion is often made precisely because the legs do not
balance. See [0047](0047-grouping-runs-as-precedence-ordered-passes.md).

**A regroup should still not churn ids gratuitously**, though nothing about human
judgement depends on it any more. The engine writes only where its partition differs
from the stored one, and since the grouping job runs after every import over a
neighbourhood far wider than the uploaded period, the difference is not marginal.

**What a person still cannot say.** An exact-token claim is a must-link, so an
assertion only ever adds. There is no way to record that two postings do *not* belong
together, which means a pairing the engine derives wrongly cannot be repaired by hand.
For the same reason an assertion cannot be outranked by evidence arriving later: it is
protected by precedence, so a source-stated grouping that contradicts it loses, and
nothing reports that it did. Both are non-goals rather than settled here; see issue
[0095](../issues/0095-match-imbalanced-groups-that-should-be-one.md).
