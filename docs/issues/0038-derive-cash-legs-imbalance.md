---
status: open
title: Route posting residuals to Imbalance
milestone: M12
dependencies: [0036, 0037, 0063]
---

Route the residual of an unbalanced group to an `IMBALANCE` posting, and
unmatched transfer sides to `TRANSFER_CLEARING`. Still no balance constraint.

**Amended.** This issue originally had the server derive the cash leg as
`-(quantity * unit_price)` from the security row. It does not: deriving a leg
double-counts for any broker that already reports its own cash row, and Fidelity
does. Grouping and leg production are the converter's job (0063, 0064; see
adr/0021-converters-own-transaction-grouping.md). What remains here is routing.

**Amended again.** The reserved accounts this issue routes to were originally
name prefixes on `txs.account`. 0037 replaced them with an `account_type` enum,
so the routing sets a type on a posting that keeps the originating broker and
account, rather than writing a posting into a differently named account.

## Motivation

Today a BUYSTOCK row records the shares but not the corresponding cash
decrease, so cash balances are only as good as whatever separate cash rows the
broker happened to supply. That blocks MWR and makes reconciliation
unreliable.

## The problem

Not every converter will supply a complete group. A broker may report no cash row
and no unit price, or report a price that does not account for a charge. Requiring
every broker converter to be correct before double-entry can be turned on would make
this change unshippable.

## The solution

Never reject. Post the group as supplied and send the residual to an explicit
`IMBALANCE` posting. This is ledger's behaviour, and it is what makes
double-entry adoptable before the source data is perfect:

- the invariant holds structurally, by construction, from day one;
- the residual becomes visible and measurable instead of being silently
  absorbed into a cash balance;
- coverage tightens per broker over time rather than in a single cut-over.

## Design

- Any group whose postings do not sum to zero gets an `IMBALANCE` posting for the
  residual. It keeps the `broker` and `account` of the group it balances, so the
  residual stays attributable to the account that produced it, which is what the
  per-broker total in 0039 reads. Its commodity is the `settlement_currency`
  instrument, falling back to `trading_currency` when settlement is absent; the
  currency is carried by `instrument_id` and is not encoded in a name.
- **Transfers** (JRNLFUND, JRNLSEC, TRANSFER): do not attempt to pair the two
  sides at ingest. Brokers report them in separate statements and matching is
  unreliable. Post each side as `TRANSFER_CLEARING`. Matching is 0068; until a
  pair is matched, a non-zero balance means an unmatched transfer, surfaced for
  review (see 0039).
- INITIALIZE synthetic transactions gain an `EQUITY` posting as their
  counterparty.

## Income and charges are the converter's job, not the residual's

0037 adds `INCOME` and `EXPENSE` types, but nothing here routes to them. A
dividend's income leg and a charge's expense leg are legs of the event, and under
adr/0021-converters-own-transaction-grouping.md the converter emits them.

The consequence is worth being explicit about, because it shapes what 0039 will
show. Until each converter emits those legs, every single-row dividend and charge
is a group that does not sum to zero, so its full value is routed here. Early
`IMBALANCE` balances will therefore be dominated by uncategorised income and
charges rather than by the missing-fee residuals the mechanism is aimed at.
That is not a reason to route them automatically -- the server cannot tell an
uncategorised income row from a genuinely incomplete trade -- but 0039 should
expect it and 0040 is where it gets fixed.
