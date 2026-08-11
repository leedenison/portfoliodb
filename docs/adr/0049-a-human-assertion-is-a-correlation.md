# A human assertion is a correlation, not an anchor

[0041](0041-server-owns-transaction-grouping.md) leaves a question open and
[0037](0037-transfer-matches-are-links-not-postings.md) restates it: group ids
churn when the partition is recomputed, so a grouping or a match a person
asserted by hand "cannot be keyed on something that evaporates". Both conclude
that human judgement has to become an input replayed on every run, "keyed on
something that outlives the groups it currently names", and both record that
what that key is remains open, because a posting has no natural key
([0002](0002-transaction-ingestion-model.md)).

**A person's grouping is recorded as a correlation on the postings it names, and
there is no key.** The asserter synthesises a token, stamps it on each member
with `SCOPE_USER` and `MATCH_EXACT`, and the engine's highest-precedence pass
([0047](0047-grouping-runs-as-precedence-ordered-passes.md)) claims them together
like any other exact match.

The open question assumed the assertion would live beside the postings and have
to find them again. It does not: a correlation is a field of a posting
([0048](0048-correlations-declare-their-own-semantics.md)), so it is already
attached to what it names. There is nothing to resolve, nothing to re-resolve
after a regroup, and no case where the thing a stored assertion points at cannot
be found. `SCOPE_USER` joins the vocabulary because none of the existing scopes
fits: `FILE` binds to an ingestion job, and `ACCOUNT` and `BROKER` are both
narrower than a manual grouping, which is most often made precisely because the
legs are in different accounts or came from different brokers.

This is what [0097](../issues/0097-server-side-transaction-grouping.md) means by
one design serving both asserters. A source states a grouping as a shared token
and the engine consumes it as evidence; a person does the same thing with a token
nobody transcribed. The engine has one kind of evidence and no special case, and
[0048](0048-correlations-declare-their-own-semantics.md)'s transcription contract
is unaffected: it binds converters, which must not manufacture evidence the
source did not supply, and says nothing about a person deliberately supplying
some.

## An assertion lives and dies with the postings it is written on

Which is the whole of its lifecycle, and each half is what is wanted.

**A re-upload destroys it.** Ingestion is idempotent by replacement
([0002](0002-transaction-ingestion-model.md)), so the postings in the period go
and the correlations on them go too. The user is told a manual grouping will be
lost before the replace runs, and is not offered a rebuild. A rebuild would have
to decide whether a row in the new statement is the row the person looked at,
which is undecidable exactly when it matters -- the case where the broker
restated the period is the case where the answer might no longer hold.

**An archive round trip preserves it.** The archive carries correlations on the
flat `Posting` in both directions, so an import re-creates the assertion along
with the postings and the engine regroups to the partition the person asserted.
That is consistent with
[0043](0043-grouping-does-not-travel-in-the-archive.md), which drops the
partition and keeps the evidence: an assertion travels as the evidence it is,
not as a shape imposed on the postings, and the importer regroups from it like
anything else.

## Considered: a content fingerprint over the posting's transcribed fields

The obvious reading of "keyed on something that outlives the groups it currently
names": digest the fields a re-upload reproduces -- broker, account, timestamp,
amounts, declared type, correlation tokens -- store assertions against that
digest, and replay them on every run. It buys the one thing the decision above
gives up, which is an assertion surviving a re-upload of its own period.

Rejected because it pays for that with a resolution step that has no good answer
when it fails. A digest either finds its postings or does not, and "does not" is
ambiguous between the row having genuinely gone and the broker having restated it
in a way the digest did not survive -- a corrected amount, a settlement date
moved by a day. Distinguishing those needs a fuzzier match, and a fuzzy match on
a person's assertion re-applies their judgement to rows they never saw. Nor is
the digest cheap: identical rows are legal
([0002](0002-transaction-ingestion-model.md) enforces no uniqueness), so it needs
an occurrence index beside it, which reintroduces exactly the positional
fragility it was meant to remove.

The correlation carries no such ambiguity, because it is not a reference. It is
present, or the posting is not.

## Consequences

**A regroup must not churn ids gratuitously.**
[0037](0037-transfer-matches-are-links-not-postings.md) reasons about id churn as
though a recomputed partition necessarily rewrites every group, and under that
reading a `MANUAL` transfer match -- which is keyed on group ids and is not a
correlation -- would break on every cycle. It does not have to: the engine
compares its partition against the stored one and writes only where the two
differ, so a group it agrees with keeps its id. Since the grouping job runs after
every import over a neighbourhood far wider than the uploaded period, the
difference is not marginal -- without it, importing one month of one broker would
destroy manual matches on postings nobody touched. See
[0047](0047-grouping-runs-as-precedence-ordered-passes.md).

**A manual grouping is protected by precedence, not by leaving it alone.** An
exact-token claim is a must-link ([0048](0048-correlations-declare-their-own-semantics.md)),
and the pass that makes it runs first, so a later pass can add to an asserted
group but can never take a member out of it. Nothing else is needed, and in
particular no rule that spares a group the engine finds already settled: such a
rule would hold whether or not a person had said anything, and would stop the
engine correcting a partition that is wrong but balances. It would also protect
the wrong groups -- an assertion is often made precisely because the legs do not
balance, which is the case a balance-based rule leaves unprotected. See
[0047](0047-grouping-runs-as-precedence-ordered-passes.md).

**A manual transfer match is a different object and keeps a different answer.** It
links two groups rather than joining postings, because the link records which
account holds the other side ([0037](0037-transfer-matches-are-links-not-postings.md)),
and merging the two groups would erase the account boundary the pairing exists to
express. So it stays keyed on group ids and relies on the stability above, rather
than becoming a correlation. What becomes a correlation is the non-transfer
repair in [0095](../issues/0095-match-imbalanced-groups-that-should-be-one.md),
which is a merge and therefore a grouping.
