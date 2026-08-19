---
status: open
title: Account hierarchy with ltree
milestone: M22
dependencies: [0037, 0108]
---

Model accounts as a hierarchy using the Postgres `ltree` type rather than flat
opaque `broker` and `account` text.

## Motivation

`txs.broker` and `txs.account` are independent opaque strings, and portfolio
membership is expressed through `portfolio_filters` with AND between categories
and OR within, implemented in the `portfolio_matched_txs` view. That works, but
every aggregation level has to be enumerated as filter rows.

A tree makes aggregation at any level free: all accounts at one broker, all
ISAs across brokers, one account, or everything, are the same query with a
different prefix.

0037 classifies non-asset postings with an `account_type` enum as the deliberate
minimum needed for double-entry. This issue is the general case: broker accounts
themselves become paths. The two compose -- the type becomes the root label of a
path and the broker account hangs beneath it -- so nothing in 0037 has to be
undone first.

## Scope of the benefit

This is portfolio-definition ergonomics, not correctness. The cash flow
boundary that MWR needs is satisfied by the account types in 0037, since
classification depends only on the type, not on broker accounts being
hierarchical. What a full tree adds is aggregation over account subtrees --
"everything at IBKR", "all ISAs across brokers" -- without enumerating a filter
row per account.

## Inspiration

Beancount's account tree, where `Assets:Broker:IBKR:Stocks` aggregates at any
depth.

## Design

- `ltree` path per account, GiST indexed, with `<@` and `@>` for
  ancestor/descendant queries. This is the idiomatic Postgres answer and avoids
  recursive CTEs or a closure table.
- Migrate existing `broker` + `account` pairs into paths.
- Replace the category matching in `portfolio_matched_txs` with prefix
  matching; `portfolio_filters` rows become path prefixes.
- Account types from 0037 become real roots in the path rather than a column on
  the posting.

## Note: decide whether this is wanted at all

Not urgent, and possibly not correct. The existing `portfolio_matched_txs` view
works, and nothing else here depends on this issue.

There is also a modelling question to settle first. The unscheduled milestone
list already contains "portfolio definition based on tagged instruments" and
"portfolio sharing between users and aggregates that combine portfolios". Tags
and aggregates are orthogonal to hierarchy -- an account belongs to exactly one
place in a tree but can carry many tags -- so migrating to `ltree` and then
discovering that portfolios are really tag-driven would be wasted work.

Resolve the tags-versus-hierarchy question before starting this. Closing it as
not-planned is a reasonable outcome; 0037 stands on its own either way.
