---
status: open
title: Match imbalanced groups that should be one
dependencies: [0068, 0097]
---

Repair, after the fact, postings that belong to one economic event but were
stored in separate unbalanced groups.

## Motivation

Grouping is the converter's job and a converter sees one file
(adr/0021-converters-own-transaction-grouping.md), so several paths leave the
legs of one event in distinct groups, each balanced by a routed residual:

- Two legs arriving in two broker logs. No converter can see both.
- A straddling group cut by the period replace in 0094.

The transfer matcher from 0068 already repairs one case of this, and is
deliberately narrow: it keys on `account_type = 'TRANSFER_CLEARING'`, requires
the same broker, an exact equal-and-opposite amount and a window of seven days,
and refuses ambiguity. Everything else stays unmatched, which reads as an
imbalance that never resolves.

## Design

Two questions, and the second is the harder one.

**Which evidence justifies a match.** Where a sufficiently reliable rule exists,
match automatically as 0068 does. Where it does not, present likely candidates
and defer to the user for the final call. A wrong pair is worse than no pair, so
the bar for automatic matching stays where 0068 put it: evidence that identifies
the occurrence, not merely the amount and a window.

**Whether a match is a link or a merge, which depends on what is being matched.**

adr/0037-transfer-matches-are-links-not-postings.md chose a link, and its reason
is specific to transfers: the link records *which account holds the other side*,
because the portfolio membership test in
adr/0022-typed-per-account-cash-flow-boundary.md asks whether both accounts are
members before it nets a pair or values what is in transit. Merging two transfer
groups would erase the account boundary money-weighted return depends on, which
is the whole reason the pairing exists.

An `IMBALANCE` residual carries no such boundary. Both halves of a split trade
sit in one account, and the residual is an artefact of the split rather than an
economic fact, so the right repair is a merge: reassign `group_id` and delete
both residuals. 0037 calls that unreachable, but that argument is about clearing
one side of a pair; `check_tx_group_balance()` is `DEFERRABLE INITIALLY
DEFERRED`, so a merge performed in one transaction is evaluated at COMMIT, where
the merged group balances.

Amend 0037 to say it is scoped to transfers and why, rather than leaving it
reading as a rule about matching in general.

## Relationship to 0097

0097 derives groups on the server from stored evidence, over the whole dataset rather
than one upload. That takes most of the first bullet above with it: two legs in two
broker logs, and a group cut by a period replace, are both cases the engine joins
without anyone being asked. What is left here is the half 0097 cannot do -- the pair
whose evidence does not identify the occurrence, where the answer is a person's
judgement rather than a rule.

## Recording the human answer

A hand-made grouping -- the merge repair above, not a transfer match -- is recorded as
a correlation on the postings it joins: a synthesised token, `MATCH_EXACT`, and
`SCOPE_USER`, which the grouping engine claims through its highest-precedence pass like
any other exact match. adr/0049-a-human-assertion-is-a-correlation.md settles this. What
lands here is the writer: the `SCOPE_USER` value in the vocabulary, the API and UI that
create and remove an assertion, and the warning shown before a re-upload that would
destroy one.

A hand-made transfer match stays a link keyed on group ids, for the reason
adr/0037-transfer-matches-are-links-not-postings.md gives, and survives on the engine
writing only the groups it disagrees with.

## A manual match whose target stops making sense

Distinct from the id churn adr/0037-transfer-matches-are-links-not-postings.md now records, and not fixed by anchoring a match to
something durable. A later upload can leave the group a manual match names in a state
where the match no longer means anything, with the same id throughout: the group's
residual changes commodity or sign, or it loses the clearing leg the match was made
against, because the re-uploaded rows genuinely differ from the ones the person looked
at.

So a match can be anchored perfectly and still be stale. Settle what happens: whether
it survives on the assumption the judgement still holds, is dropped as unsupported, or
is kept and flagged for review. The last is probably right -- a person's answer is
expensive to obtain and discarding it silently is worse than showing it with a caveat
-- but the condition that triggers the flag has to be stated, and it is not simply "the
group is imbalanced", because an unmatched transfer is imbalanced by construction.

## Relationship to 0091

0091 covers hand-pairing transfers the matcher left alone and breaking pairs it
got wrong, on the transfers view of `/admin/imbalance`. This is that generalised
past transfers, with a different repair for the non-transfer case. Decide whether
0095 subsumes 0091 or sits on top of it before either is picked up.
