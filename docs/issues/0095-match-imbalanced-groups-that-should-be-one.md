---
status: open
title: Match imbalanced groups that should be one
milestone: M16
dependencies: [0068, 0097]
---

Let a person repair, after the fact, postings that belong to one economic event but
were stored apart, and record the answer as evidence the engine consumes.

## Motivation

0097 derives groups on the server from stored evidence, over the whole dataset
rather than one upload, so the cases that used to need a person no longer do: two
legs arriving in two broker logs, and a group cut by a period replace, are both
joined without anyone being asked.

What is left is the half no engine can do. A pair is matched automatically only on
evidence that identifies the *occurrence*, because a wrong pairing is worse than no
pairing (adr/0037-transfer-matches-are-links-not-postings.md), and where the
evidence does not reach that bar the answer is a person's judgement rather than a
rule. Today there is nowhere to put that judgement, so it reads as an imbalance that
never resolves, or as a transfer that stays in flight for ever.

Which evidence justifies an automatic match is settled and is no longer a question
for this issue. The engine states it as five rules in a fixed precedence order --
`Exact`, `Disposal`, `Acquisition`, `CashTrade`, `Deposit` in `server/grouping/` --
each gated by 0044's *may be* test, each ranking its candidates globally before it
claims, and each leaving ambiguity unclaimed. See
adr/0047-grouping-runs-as-precedence-ordered-passes.md and the "Where grouping is
decided" section of docs/spec/postings.md.

## Design

**One evidence shape for both repairs, and the type decides what it means.**

A hand-made grouping and a hand-made transfer pairing are the same object: a token
the asserter synthesises, stamped on each named posting with `MATCH_EXACT` and
`SCOPE_USER`, exactly as adr/0049-a-human-assertion-is-a-correlation.md describes.
What differs is what the engine concludes from it, and that follows from the
postings' own type rather than from anything the assertion says:

- **Every named posting must be a transfer.** The assertion pairs the two sides. The
  grouping engine declines to claim them, so each side keeps its own group and its
  `TRANSFER_CLEARING` residual, and the transfer matcher writes the link. Merging
  them would erase the account boundary
  adr/0022-typed-per-account-cash-flow-boundary.md asks about before it nets a pair
  or values what is in transit, which is the whole reason 0037 chose a link.
- **No named posting is a transfer.** The assertion merges them: one group, one
  residual, routed against the membership that now holds. An `IMBALANCE` residual
  carries no account boundary -- both halves of a split trade sit in one account and
  the residual is an artefact of the split -- so a merge is the honest repair.
- **Anything else is rejected when it is asserted**, and the person is told to
  correct the declared type and re-upload. That covers a mixed set, and a transfer
  assertion whose sides sit in one account.

No new `Match` operator is needed for this. `MATCH_ACCOUNT` was considered and does
not fit: it names an account rather than an occurrence, so it cannot say which of
that account's sides is meant, which is the ambiguity a person is being asked to
resolve in the first place.

**A hand-made transfer link is derived, not stored by hand.** It stops being state
written once and preserved, and becomes something the matcher rebuilds from the
correlations on every cycle, like `POINTER` and `REFERENCE`. It has to: `applyChanges`
in server/db/postgres/grouping_apply.go runs `deleteTouchedMatchesSQL` over every
group a posting left and every group the engine created, and nothing rebuilds a
`MANUAL` row, so a link written by hand is destroyed by any genuine repartition of
either side. A cycle that agrees with the stored partition still churns nothing, so
this is a gap in what 0049 assumed rather than a contradiction of 0047.

## What lands

- `SCOPE_USER` in `type.v1.Scope` (proto/type/v1/type.proto), in the
  `tx_correlations.scope` CHECK (server/migrations/001_initial.sql, amended in
  place), and as `db.ScopeUser`.
- A user assertion's comparability set is this user's whole dataset, so its
  `within` key is the user and its reach widens past broker as well as account.
- A rule above `Exact`, at precedence 1100. Distinct rather than folded into
  `Exact` because its precedence over a source-stated token has to be stated rather
  than emerge from key sort order (0047: precedence is data on the rule), because
  its reach is unbounded where `Exact`'s is not, and because it declines a transfer
  assertion instead of claiming it.
- A pass in server/transfermatch/match.go above the pointer pass, matching two
  sides whose groups share a `SCOPE_USER` token and writing `method = 'MANUAL'`. It
  bypasses the same-broker, equal-amount and seven-day gates deliberately: those
  reject precisely the cross-broker, fee-differing, slow transfer a person is being
  asked about. Direction follows the existing convention, `from` being the side
  holding the positive `TRANSFER_CLEARING` residual.
- An API to create and delete an assertion, enforcing the rejections above and one
  more: it refuses to stamp a token on a posting that already carries a `SCOPE_USER`
  one. That is a correctness rule, not tidiness. `State.Claim` is all-or-nothing and
  the exact rule iterates its keys in sorted order, so two overlapping user tokens
  do not union -- whichever sorts first takes its postings and the other assertion
  is discarded in silence. A second assertion overlapping the first is refused at
  write time, and the person removes the old one.
- A UI on /admin/imbalance to make and remove an assertion from both the imbalance
  view and the transfers view. The imbalance view aggregates balances today and
  says outright that drilling into the groups behind one is not implemented, so the
  drill-down is part of this.
- The destruction rule and its warning, below.
- docs/spec/postings.md: `SCOPE_USER` in the correlation scope table, the assertion
  rule under "Where grouping is decided", and the destruction rule under
  "Deletion".

## An assertion is destroyed when its postings change

Two guarantees, and deliberately no more:

1. A person is warned before an import commits that would modify a posting
   carrying a user-asserted correlation.
2. When such a posting is modified, that correlation and every other user-asserted
   correlation sharing its token are deleted.

So the assertion is never partly alive, and the person re-asserts if they still want
it. That is the price of not building the alternative, which is a resolution step
with no good answer whenever it fails -- 0049 sets out why a fuzzy re-anchoring
re-applies a person's judgement to rows they never saw.

Deleting by token also means an assertion never has to record how many postings it
named: `idx_tx_correlations_token` already indexes the lookup that finds its
siblings.

**Ordering is what preserves the archive round trip.** The replace path collects the
tokens carried by the postings it is about to delete, deletes the postings, deletes
the correlations that survive elsewhere carrying those tokens, and only then inserts
the incoming postings with their own correlations. So the deletion reaches what
pre-dated the write and nothing the write itself supplied, and an archive that
carries a whole assertion re-creates it, as adr/0043-grouping-does-not-travel-in-the-archive.md
and 0049 both promise.

**The warning is a client-side preflight.** Nothing in the ingestion path gates a
commit today: `UpsertTxs` queues a job and returns a `job_id`, and `JobStatus` has no
state a user acknowledges. The precedent to follow is `CountIgnoredTxs`, a read-only
count the client calls before the mutating call to raise a confirm dialog. So a
read-only RPC takes the window's broker and period, plus any `SCOPE_USER` tokens the
payload itself carries, and returns the assertions that would be destroyed together
with how many postings *outside* the window lose a correlation -- that last number
being the part a person will not expect. The upload modal calls it after the
client-side parse and before the upload. The server does not block: an import from
cron or the CLI has nobody to ask.

## Non-goals

**A person cannot record a negative.** An exact-token claim is a must-link, so an
assertion can only ever add. There is no way to say "these two are not one event",
which means a pairing the engine derives wrongly cannot be repaired by hand and a
link it wrote cannot be broken -- it is re-derived on the next cycle from the same
evidence. Known gap, not an oversight.

**An assertion cannot be overridden by evidence that arrives later.** A person
answers in the absence of evidence, and a later upload can supply some -- a source
stating a grouping of its own that overlaps theirs. Precedence settles it in the
person's favour, and `State.Claim` being all-or-nothing means the source's claim is
then refused whole rather than partly honoured: its other members fall to the weaker
rules. Nothing reports that this happened.

## Suggested split

Three increments rather than one change:

1. Vocabulary and engine -- `SCOPE_USER`, the assertion rule, the matcher's pass,
   and the destruction rule in the replace path.
2. API -- create and delete, the preflight count, and a drill-down listing the
   postings behind a residual balance.
3. UI -- selection and the two actions on both views of /admin/imbalance, and the
   confirm dialog in the upload modal.

## Relationship to 0091

0091 is subsumed. Its create half is the transfer case above, sharing one writer
with the non-transfer repair; its unmatch half is the non-goal above.
