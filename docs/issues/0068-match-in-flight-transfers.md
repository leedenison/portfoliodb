---
status: closed
title: Match the two sides of a transfer and clear them from in-flight
milestone: M12
dependencies: [0037, 0038]
---

Match the two sides of a transfer so that a residual `TRANSFER_CLEARING` balance
means a genuinely unmatched transfer, and so that money-weighted return can tell
an internal transfer from an external flow.

## Motivation

0038 posts each side of a JRNLFUND, JRNLSEC or TRANSFER against a clearing
account and deliberately does not pair them at ingest, because brokers report the
two sides in separate statements and sometimes in separate imports. It defers the
pairing to "a later matching job" that no issue covered.

Without that job the clearing account only ever grows. Every transfer ever
imported sits in it, so the unmatched-transfer signal in 0039 and the alert in
0066 fire on correctly imported data and carry no information.

0039 shipped on that basis: its transfers view lists every imported transfer and
says on the page that it cannot tell a settled one from an unmatched one. When
this lands, that view can report unmatched transfers, its caveat comes off, and
the age it shows becomes the age of something actually missing. The dashboard
count can then flag rather than merely state.

It is also a correctness prerequisite, not only hygiene. Under 0037 the cash-flow
boundary is per account and netting happens per portfolio at query time, so an
unmatched transfer between two accounts of the same portfolio reads as a
withdrawal followed by an unrelated deposit. MWR for any multi-account portfolio
is wrong until the pair is matched.

## Prerequisite: capture the counterparty and the broker reference

`txs` stores no broker transaction reference, no counterparty account and no memo
(`instrument_description` is "Cash" for every one of these rows). On what is
stored today, matching reduces to equal-and-opposite amount within a date window
between two accounts -- which is exactly the ambiguous case this issue refuses to
guess at. Fidelity's monthly fee transfers are the adversarial case: the same two
small amounts recur between the same account pairs every month for the whole
sample, so only the date distinguishes one month's pair from another's.

The source data carries what is needed and the converters discard it:

- **Fidelity JSON** has `sourceOrTargetAccount`, naming the counterparty account
  explicitly. It is populated on 40 of the 424 records in
  `local/HAR/transactions-long-window.json` and read nowhere in `client/` or
  `extension/`. With it stored, Fidelity matching is a lookup rather than a
  heuristic.
- **`referenceId`** is already parsed by the Fidelity converter as `ref` and used
  as a proximity tiebreak when grouping, but never persisted. The two sides of a
  transfer carry adjacent references.
- **IBKR QFX** has `FITID` on every transaction, a stable broker-side id. It has
  no counterparty field, and OFX offers none for these records.

Carrying both through the upload format into `txs` is part of this issue. The
reference is independently useful: it is the natural key for de-duplicating a
re-imported statement.

## What the data looks like

The two sides are asymmetric -- only the receiving side names the counterparty.
A lump sum moved from a fund account to an ISA arrives as five rows across two
hops, every row carrying the same amount. Accounts are named here by product
type rather than by number:

| account                 | type                                      | dr/cr  | source/target   |
| ----------------------- | ----------------------------------------- | ------ | --------------- |
| Investment Fund         | Transfer To Cash Management Account        | DEBIT  | --              |
| Cash Management Account | Transfer Into Account                      | CREDIT | Investment Fund |
| Cash Management Account | Transfer Out From Cash Management Account  | DEBIT  | --              |
| ISA                     | Cash In Lump Sum                           | CREDIT | --              |
| ISA                     | Cash In                                    | CREDIT | --              |

So a match is found by reading the target from the side that names it and
inferring the source, not by pairing two symmetric records. The monthly fee
transfers follow the same shape: a debit from the product account, a
`Cash In Ring-fenced For Fees` credit into the cash management account, and then
a separate `Service Fee` debit there that names the product account it was
charged for.

Note also that every transfer evidenced in the sample data is **intra-broker,
with both sides in the same import**. The split-across-uploads case that 0038
designed for is real in principle -- a cross-broker transfer -- but there is no
sample of one, so it cannot be calibrated against the masters the way the
existing converter pairing rules were.

The last two rows of that table are two of the three rows a deposit into a product
account is reported through, the third being a debit that cancels one of them.
0069 settles that: the three are one group, so the ISA offers this issue a single
`TRANSFER_CLEARING` arrival against the cash management account's equal and
opposite departure. Before it the account offered two identical arrivals, either
of which the matcher could have taken, leaving the other unmatched and therefore
external for good.

## Design

- Prefer the explicit pointer. Where the source names the counterparty, matching
  is exact and needs no window or tolerance.
- Fall back to candidate pairing only where no pointer exists: same user, same
  commodity, equal and opposite quantity, different accounts, within a date
  window, with broker reference proximity as a tiebreak. JRNLSEC moves securities
  rather than cash, so the commodity is not always a currency.
- Settle the window. The two sides are dated by their own statements and need not
  agree; in the sample the deal dates match but the settlement dates differ.
- Leave ambiguity unmatched. Two transfers of the same amount between the same
  pair of accounts in the same window are not distinguishable, and surfacing both
  is better than pairing arbitrarily.
- Re-runnable, because the second side can arrive in a later import than the
  first.

### Recording a match

A match records **which account the other side is in**, not merely that a match
exists, because that identity is what the portfolio membership test in 0037
consumes.

Store it as a link between the two groups: the two group ids, how they were
matched (explicit pointer, heuristic, manual) and when. Not a status column on
the posting, which cannot express which row it paired with. Not a synthetic third
group closing both sides out, which fabricates an economic event that never
happened and has to be unpicked on rematch.

The link is derived and disposable. A re-upload replaces one side's groups
(adr/0002-transaction-ingestion-model.md), so it cascades on group delete and the
matcher runs again. It is never authoritative and is always cheap to rebuild.

## Note

0048, now closed, was a different problem in the same area: the two legs of a
transfer carried the wrong sign, so the pair added twice its value instead of
netting to zero. Matching could not have fixed it -- with both legs the same sign
there is no equal-and-opposite pair to find. Worth remembering as a failure mode
if a converter regresses, since it presents as an unmatched transfer.
