---
status: closed
title: Fidelity cash rows typed as Sell become bogus security transactions
milestone: M12
---

The client Fidelity converter turned 13 rows in the sample export into `SELLSTOCK`
transactions against an instrument described as "Cash".

## The problem

Fidelity models an account's cash as a tradable asset, so money leaving an account
is reported as a sale of cash and money arriving as a purchase of it. Both
converters mapped on `Transaction type` alone, so each of those rows became a
security position bought or sold in units of money.

The rows arrive as a triplet with consecutive reference numbers -- the movement,
the trade of cash, and its cash leg -- and the last two net to zero. Posting the
trade of cash onto a phantom security left only the cash leg, so an account that
transferred 401 out netted nothing on the day while the receiving account still
recorded the credit. 22 rows across the two sample exports, the largest 200,500.
`local/scripts/convert-fidelity.py` had the same error by the opposite route: it
dropped the `Sell` row and kept the orphan credit.

## What was done

Classify on the asset rather than the transaction type. The CSV export says so in
its `Type` column; the extension payload has no equivalent, and the answer there
is the ISIN check digit, which `isValidIsin` already computed to keep Fidelity's
cash pseudo-identifiers out of the security master. A trade of cash is a
`CASHFLOW` that groups with the cash row beside it and weighs nothing, leaving the
withdrawal or transfer as the money that moved.

The two converters also disagreed about which rows survived. That is now closed:
eleven types the client rejected are mapped, a cancelled row is skipped by both,
and the client reads the ISO dates one of the two exports carries throughout --
it had been rejecting all 674 of its rows over a date format the source uses in
the column beside it. Both converters now produce identical output on both master
exports.

Cash postings carry their currency as the description and the `CURRENCY`
identifier hint, per docs/spec/csv-format.md, so nothing is described as "Cash"
any more. See 0049 for what that means for dividend attribution.

Found while adding transaction grouping (0064). It predated that work: grouping
neither caused it nor made it worse.
