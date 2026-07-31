---
status: open
title: Route posting residuals to Imbalance
milestone: M12
dependencies: [0036, 0037, 0063]
---

Route the residual of an unbalanced group to `Imbalance:<currency>`, and unmatched
transfer sides to `Transfers.InFlight`. Still no balance constraint.

**Amended.** This issue originally had the server derive the cash leg as
`-(quantity * unit_price)` from the security row. It does not: deriving a leg
double-counts for any broker that already reports its own cash row, and Fidelity
does. Grouping and leg production are the converter's job (0063, 0064; see
adr/0021-converters-own-transaction-grouping.md). What remains here is routing.

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
`Imbalance:<currency>` account. This is ledger's behaviour, and it is what
makes double-entry adoptable before the source data is perfect:

- the invariant holds structurally, by construction, from day one;
- the residual becomes visible and measurable instead of being silently
  absorbed into a cash balance;
- coverage tightens per broker over time rather than in a single cut-over.

## Design

- Any group whose postings do not sum to zero gets an `Imbalance:<currency>`
  posting for the residual, denominated in `settlement_currency` and falling back
  to `trading_currency` when settlement is absent.
- **Transfers** (JRNLFUND, JRNLSEC, TRANSFER): do not attempt to pair the two
  sides at ingest. Brokers report them in separate statements and matching is
  unreliable. Post each side against `Transfers:InFlight`; a later matching
  job nets them to zero. A non-zero balance there means an unmatched transfer,
  surfaced for review (see 0039).
- INITIALIZE synthetic transactions gain `Equity:Opening-Balances` as their
  counterparty.
