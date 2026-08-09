---
status: open
title: Match imbalanced groups that should be one
dependencies: [0068]
---

Repair, after the fact, postings that belong to one economic event but were
stored in separate unbalanced groups.

## Motivation

Grouping is the converter's job and a converter sees one file
(adr/0021-converters-own-transaction-grouping.md), so several paths leave the
legs of one event in distinct groups, each balanced by a routed residual:

- Two legs arriving in two broker logs. No converter can see both.
- A straddling group split by the partial delete in 0094.

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

## Relationship to 0091

0091 covers hand-pairing transfers the matcher left alone and breaking pairs it
got wrong, on the transfers view of `/admin/imbalance`. This is that generalised
past transfers, with a different repair for the non-transfer case. Decide whether
0095 subsumes 0091 or sits on top of it before either is picked up.
