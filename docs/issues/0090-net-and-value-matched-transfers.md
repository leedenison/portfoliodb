---
status: closed
title: Net matched transfer pairs and value matched in-flight balances
milestone: M12
dependencies: [0068]
---

Consume the transfer matches 0068 records: net a matched pair in a portfolio's cash
flows when both accounts are members, and include the value in transit in valuation
for the days between the two sides.

## Motivation

0068 supplies the pairing and makes the unmatched-transfer report mean something, but
nothing reads `transfer_matches`. The correctness consequence adr/0022 names is still
open: under the per-account cash-flow boundary, netting happens per portfolio at query
time, so a transfer between two accounts of one portfolio still reads as a withdrawal
followed by an unrelated deposit. Money-weighted return is wrong for any multi-account
portfolio until this lands.

## Design

Two consumers, both keyed on the match:

- **Netting.** A `TRANSFER_CLEARING` posting is external to its account while
  unmatched. Once matched, it nets against the other side when both accounts belong to
  the portfolio being measured, and stays external when only one does -- which is the
  correct answer, since from that portfolio's point of view the money really did leave.
- **Valuation.** `server/db/postgres/valuation.go` reads `account_type = 'USER'` only.
  Excluding a matched pair's clearing legs makes the transferred holding vanish for the
  days between the two statements: a dip in portfolio value and a fake return blip.
  Include in-flight value for a matched pair where both accounts are members, and
  exclude it otherwise -- including an unmatched balance would assert the money is
  coming back to a member account, which is the thing that is not known.

See docs/spec/postings.md and adr/0037-transfer-matches-are-links-not-postings.md.
