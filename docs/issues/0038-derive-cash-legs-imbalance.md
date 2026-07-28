---
status: open
title: Derive cash legs at ingestion and route residuals to Imbalance
dependencies: [0036, 0037]
---

Ingestion produces a group of postings per source row rather than a single tx.
Derive the cash leg from the security leg and route any residual to
`Imbalance:<currency>`. Still no balance constraint.

## Motivation

Today a BUYSTOCK row records the shares but not the corresponding cash
decrease, so cash balances are only as good as whatever separate cash rows the
broker happened to supply. That blocks MWR and makes reconciliation
unreliable.

## The problem

The standard CSV (docs/spec/csv-format.md) cannot express a balanced
transaction:

- there is no `fees` or `commission` column, so `quantity * unit_price` is the
  gross consideration, not the cash movement -- the derived leg is wrong by the
  fee;
- `unit_price` is optional, so sometimes the cash leg cannot be derived at all.

Requiring every broker converter to be correct before double-entry can be
turned on would make this change unshippable.

## The solution

Never reject. Derive what can be derived and post the residual to an explicit
`Imbalance:<currency>` account. This is ledger's behaviour, and it is what
makes double-entry adoptable before the source data is perfect:

- the invariant holds structurally, by construction, from day one;
- the residual becomes visible and measurable instead of being silently
  absorbed into a cash balance;
- coverage tightens per broker over time rather than in a single cut-over.

## Design

- Cash leg: `-(quantity * unit_price)` denominated in `settlement_currency`,
  falling back to `trading_currency` when settlement is absent.
- Where `unit_price` is absent, emit only the security leg and let the whole
  consideration fall to `Imbalance`.
- Amount elision follows beancount: state one side, infer the other.
- **Transfers** (JRNLFUND, JRNLSEC, TRANSFER): do not attempt to pair the two
  sides at ingest. Brokers report them in separate statements and matching is
  unreliable. Post each side against `Transfers:InFlight`; a later matching
  job nets them to zero. A non-zero balance there means an unmatched transfer,
  surfaced for review (see 0039).
- INITIALIZE synthetic transactions gain `Equity:Opening-Balances` as their
  counterparty.
