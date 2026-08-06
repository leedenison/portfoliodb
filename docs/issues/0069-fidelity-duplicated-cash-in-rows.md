---
status: closed
title: Group the run a Fidelity deposit into a product account is reported through
milestone: M12
---

Money paid into a Fidelity product account is reported as three rows of the same
amount and the converter left all three ungrouped, so the account reported twice
the contribution it received.

## Evidence

The sequence below is verbatim from `local/HAR/transactions-long-window.json` and
from both master CSV exports (2025-04-15, every row `Type=CASH`, every amount
GBP 20,000). Accounts are named by product type; references by their last three
digits.

| ref  | account         | type                                      | dr/cr  | emitted  |
| ---- | --------------- | ----------------------------------------- | ------ | -------- |
| ...528 | Investment Acct | Transfer To Cash Management Account      | DEBIT  | TRANSFER |
| ...529 | Investment Acct | Sell (of Cash)                           | DEBIT  | CASHFLOW |
| ...530 | Investment Acct | Cash In From Sell                        | CREDIT | CASHFLOW |
| ...531 | Cash Mgmt       | Transfer Into Account                    | CREDIT | TRANSFER |
| ...545 | ISA             | Cash In Lump Sum                         | CREDIT | JRNLFUND |
| ...546 | ISA             | Cash Out For Buy                         | DEBIT  | CASHFLOW |
| ...547 | Cash Mgmt       | Transfer Out From Cash Management Account | DEBIT  | TRANSFER |
| ...548 | ISA             | Cash In                                  | CREDIT | JRNLFUND |

No security appears anywhere in it. The `Cash Out For Buy` at ...546 has no `Buy`:
the ISA's real trade that day is a `Buy` of SMGB for 19,976.70 (...611) with its
own `Cash Out For Buy` of 19,969.20 (...610), both settling two days later, so the
trade pairing already keeps them apart.

The cash was never doubled. The ISA nets +20000 - 20000 + 20000, the cash
management account nets zero, the investment account nets -20000. What was wrong
was the grouping: the three ISA rows carried no `group_ref`, so each was a
single-posting group with a full-size residual, and `routeResiduals` sent ...545
and ...548 to `TRANSFER_CLEARING` and ...546 to `IMBALANCE`. An unmatched
`TRANSFER_CLEARING` posting is an external flow
(adr/0022-typed-per-account-cash-flow-boundary.md), so the ISA reported 40,000 of
contribution for a 20,000 deposit and dropped a spurious -20,000 into the
converter-lossiness report.

## What was established

- **The two credits are not the same money posted twice.** They are two of three
  rows recording one deposit, and the third cancels one of them. Confirmed by
  arithmetic across all three accounts rather than by reading the row names; the
  export carries no running balance to check against.
- **The CSV carries the same shape as the JSON.** The pattern occurs 21 times
  across the two master exports and is the ordinary shape of a deposit into a
  wrapper, not something the JSON route invents.
- **No row should be dropped.** All three are real postings and all three are
  needed for the account's cash to come out right. They belong in one group.

## Fix

A deposit pass in `assignFidelityGroups`, after the trade passes so it only ever
sees cash rows no trade wanted. It anchors on `Cash In Lump Sum` and takes the
nearest unclaimed `Cash Out For Buy` and `Cash In` in the same account, equal in
amount to the penny, at a higher reference within `DEPOSIT_REF_SPAN`.

It keys on the reference run rather than on the `(account, dateKey)` bucket the
trade passes use, because one run in the sample settles across two days while the
reference run holds in all 21.

Across both masters: 21 of 24 `Cash In Lump Sum` rows group into a run, no trade
group is disturbed, and no `Cash In` or `Cash Out For Buy` is left unexplained.
The three that stay alone are deposits straight into the cash management account
-- money from outside with nothing beside it to cancel -- and they still post as a
single `JRNLFUND`. Every field of the converted masters except `group_ref` is
unchanged.

The group keeps its `JRNLFUND` legs, so its residual routes to
`TRANSFER_CLEARING`: one arrival of the deposit amount against the cash management
account's equal and opposite departure, which is the pair 0068 has to match. It is
the same shape a lone `Transfer Out` already produces, so both sides of the hop
look alike.

## Adjacent, explained by 0065

`Cash In For Transfer` and `Cash Out For Buy From Transfer` appear as equal and
opposite same-account, same-day pairs, twice, in the SIPP. They are not a pair:
each sits in a triplet with a `Buy` row against Fidelity's cash pseudo-ISIN, and
it is that `Buy` the `Cash Out For Buy From Transfer` cancels. 0065 types the
`Buy` as the cash movement it is and groups the two, which leaves the
`Cash In For Transfer` posting once, as the arrival it is. Nothing here is a
duplicate, and the deposit pass does not touch it.

## Left as it is

`routedFor` stamps a residual with the type of the group's first posting, which
for a deposit is whichever of the three the export listed first -- often the
`Cash Out For Buy`. So the routed posting can read `CASHFLOW` while its account
type is `TRANSFER_CLEARING`. The account type is what classifies the flow, so this
only affects the `tx_type` column the imbalance report groups by.
