---
status: closed
title: Typed account classification for non-asset postings
milestone: M12
dependencies: [0036]
---

Add an `account_type` to postings that classifies the account they land in, so
that the non-asset side of a one-sided event has somewhere to go and the
portfolio cash-flow boundary is a typed predicate rather than a string parse.
See adr/0022-typed-per-account-cash-flow-boundary.md.

## Motivation

Double-entry needs somewhere to post the other side of events that are one-sided
in the source data:

- **INITIALIZE synthetic transactions** (docs/spec/fixed-point.md, ADR 0011)
  are one-sided by construction. A pad has no counterparty.
- **Income and charges** arrive as a single cash row from most brokers. A
  Fidelity dividend is one credit; an IBKR cash dividend is one `INVBANKTRAN`.
  The converter has to post the other leg somewhere.
- **Derived cash legs** will not always balance, because the standard CSV
  carries no fees and `unit_price` is optional (see 0038, 0040).
- **Transfers** (JRNLFUND, JRNLSEC, TRANSFER) are inherently two-account and
  brokers report each side in a separate statement, sometimes in different
  imports.

Income and expenses matter more than they look. Without a type to post them to,
every dividend and every charge is a single-posting group that does not sum to
zero, so under 0038 its whole value lands in `IMBALANCE`. The imbalance report
in 0039 is meant to measure how lossy each converter is; it would instead be
dominated by correctly imported income.

## Design

`txs` gains an `account_type` enum. A posting keeps `broker` and `account`
pointing at the broker account the event belongs to; `account_type` says what
kind of leg it is:

| Value               | Meaning                                                   |
| ------------------- | --------------------------------------------------------- |
| `USER`              | An ordinary broker account posting. The default.           |
| `EQUITY`            | Value entering or leaving the user's holdings entirely.    |
| `INCOME`            | Dividends, interest and other income.                      |
| `EXPENSE`           | Commissions, levies, custody and service charges, taxes.   |
| `IMBALANCE`         | The residual of a group that does not sum to zero (0038).  |
| `TRANSFER_CLEARING` | One side of a transfer whose other side is not yet known.  |

Opening balances are `EQUITY` postings rather than an `OPENING_BALANCE` type of
their own. Opening balances are one use of equity; ADR 0011 already anticipates
other synthetic kinds such as TRUE_UP, and a withdrawal to a bank is an equity
posting too. An enum that mixes roots with specific accounts ages badly.

### Typed, not a naming convention

An earlier version of this issue used reserved name prefixes on `txs.account`
(`Equity.Opening_Balances`, `Imbalance.<currency>`, `Transfers.InFlight`), after
beancount's account roots and ledger's automatic `Imbalance:<CUR>` that absorbs
a residual rather than rejecting it. The behaviour is kept; the encoding is
replaced, for three reasons:

- **`account` is user-supplied free text.** A broker CSV containing an account
  named `Imbalance.USD` collides with the machinery, and every read path has to
  defend itself with a `NOT LIKE` that no index helps.
- **Attribution.** `Imbalance.USD` as an account name has nowhere to record
  which broker account the residual came from, yet 0039 needs exactly that: a
  per-broker total is its headline number. Keeping `broker` and `account` on the
  posting and adding a type gets attribution for free.
- **The currency is already modelled.** Currencies are instruments and a
  posting's commodity is `instrument_id` (docs/spec/postings.md), so an
  imbalance in USD is a posting of the USD currency instrument. Encoding the
  currency in a name duplicates a typed, foreign-keyed column in a string the
  two can disagree with. The 0039 breakdown is
  `GROUP BY broker, account, instrument_id`. It also handles JRNLSEC, where the
  commodity is a security and there is no currency to name.

This drops the previous rationale that dot-separated names are valid `ltree`
labels and so would migrate into 0046 unchanged. That is an acceptable loss:
0046 says of itself that it is "not urgent, and possibly not correct" and that
closing it as not-planned is a reasonable outcome. The enum composes with a tree
in any case -- the type becomes the root label and the broker path hangs beneath
it.

### Uniqueness

Constraining an account to be unique per type needs an accounts table, and
accounts today are implicit: distinct `(broker, account)` pairs in `txs`, with
`portfolio_filters` matching the raw strings. Promoting accounts to a table is
most of what 0046 would do. Until then `account_type` is a column on the
posting and `(broker, account, account_type)` is the account identity by
convention rather than by constraint.

## The cash-flow boundary

Classification is **per account and fixed at ingest**. Netting is **per
portfolio and resolved at query time**.

Per-account classification is what makes this implementable. When a transfer's
first side is posted we may not know whether the other side is an account we
hold, or whether it exists in our data at all -- that is the whole reason
`TRANSFER_CLEARING` exists, and a rule needing an answer we do not have cannot
be applied. It also has to be stable: portfolios are views over editable
`portfolio_filters` (ADR 0010), so a portfolio-relative classification would let
adding an account to a portfolio silently reclassify historical flows and move
every past return figure.

A posting in account A is an external flow of A iff its group has a leg outside
A. `account_type` says what kind of outside:

- `EQUITY` -- external, and never nets, since there is no counterparty side.
- `INCOME`, `EXPENSE`, `IMBALANCE` -- not flows. These are return and cost, and
  treating them as external would strip dividends out of the return and report
  it gross of fees. `IMBALANCE` is internal because a residual is usually a
  missing fee or a missing cash leg, both of which are internal, and because
  classing it external would quietly launder bad data out of the return instead
  of leaving it visible as 0039 intends.
- Another `USER` account -- external to A, and nets against the other side when
  both accounts belong to the portfolio being measured. This covers a buy in one
  account settled from cash in another, not only the transfer types.
- `TRANSFER_CLEARING` -- external while unmatched, because one half is all we
  know. It nets once 0068 has matched the pair and both accounts are members.

This makes 0068 a prerequisite for correct MWR on any multi-account portfolio
rather than the housekeeping job it was first written up as.

Membership decides internal versus external; there is deliberately no
per-portfolio user override. Membership already expresses the intent, and a
toggle would be a second place to say the same thing that can disagree with the
first.

Differing dates on the two sides resolve themselves. When both accounts are
members the pair nets and the dates are moot; when only one is a member, that
side's own date is the flow date.

## Visibility

Non-`USER` types are excluded from holdings and portfolio views by default, so
no user sees an `Imbalance` or in-flight position among their holdings. The
predicate is `account_type = 'USER'`.

Valuation is a different question. When a portfolio contains both sides of a
matched transfer and the two sides are dated by their own statements, excluding
`TRANSFER_CLEARING` makes the transferred holding vanish for the days in between
-- a dip in portfolio value and a fake return blip. Holding value in transit is
what a clearing account is for. So: exclude from holdings display always;
include in valuation only for matched pairs where both accounts are members.
Including an unmatched in-flight balance would assert the money is coming back
to a member account, which is the thing we do not know.
