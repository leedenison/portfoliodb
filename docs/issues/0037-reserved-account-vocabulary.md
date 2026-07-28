---
status: open
title: Reserved non-asset account vocabulary
---

Introduce a small set of reserved account names that are not broker accounts:
`Equity.Opening_Balances`, `Imbalance.<currency>`, `Transfers.InFlight`.

## Motivation

Double-entry needs somewhere to post the other side of events that are
one-sided in the source data. Three cases exist today:

- **INITIALIZE synthetic transactions** (docs/spec/fixed-point.md, ADR 0011)
  are one-sided by construction. A pad has no counterparty.
- **Derived cash legs** will not always balance, because the standard CSV
  carries no fees and `unit_price` is optional (see 0038, 0040).
- **Transfers** (JRNLFUND, JRNLSEC, TRANSFER) are inherently two-account and
  brokers report each side in a separate statement, often in different imports.

## Inspiration

Beancount's five account roots (Assets, Liabilities, Income, Expenses, Equity)
and its use of `Equity:Opening-Balances` as the counterparty for `pad`.
Ledger's automatic `Imbalance:<CUR>` account, which absorbs the residual of an
unbalanced transaction rather than rejecting it.

## Design

- A reserved-prefix convention on `txs.account` rather than a full account
  tree. Accounts are per-user.
- Reserved accounts must be excluded by default from holdings, portfolio
  views, and valuation, so they do not appear as spurious positions.

**Naming is dot-separated and restricted to alphanumerics and underscore** --
`Equity.Opening_Balances`, not beancount's `Equity:Opening-Balances`. This is
deliberate: these are valid `ltree` paths, so if the account model later moves
to a real hierarchy (0046) the stored strings migrate with a column type change
and no rewrite. Colons are not valid `ltree` labels, and hyphen support varies
by PostgreSQL version, so both are avoided.

## Sufficient for the cash flow boundary

A reserved *root* is all that money-weighted return needs: a flow is external
iff it crosses between a broker account and one of `Equity`, `Income`,
`Expenses` or `Imbalance`. That classification does not require broker accounts
themselves to be hierarchical, so this issue -- not 0046 -- is what unblocks
MWR.
