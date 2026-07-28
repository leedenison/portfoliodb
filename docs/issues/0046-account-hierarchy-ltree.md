---
status: open
title: Account hierarchy with ltree
dependencies: [0037]
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

0037 introduces a reserved-prefix convention for non-asset accounts
(`Equity.`, `Imbalance.`, `Transfers.`) as the deliberate minimum needed for
double-entry, using dot-separated `ltree`-valid names so that those strings
migrate here unchanged. This issue is the general case: broker accounts
themselves become paths.

## Scope of the benefit

This is portfolio-definition ergonomics, not correctness. The cash flow
boundary that MWR needs is satisfied by the reserved roots in 0037, since
classification depends only on the root, not on broker accounts being
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
- Reserved accounts from 0037 become real roots rather than a naming
  convention.

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
