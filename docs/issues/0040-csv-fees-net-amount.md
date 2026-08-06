---
status: closed
title: Emit fee and cash postings from brokers that net commission
milestone: M12
dependencies: [0039, 0063]
---

Update the broker converters that report only a net total to split out the
commission and emit it as its own posting.

**Amended.** This issue originally added `fees` and `net_amount` columns to the
standard CSV. It no longer does. A fee is a posting with `type=INVEXPENSE`, not a
column: that is already how Fidelity's separately-reported charges arrive, and
columns would express the same money twice. See
adr/0021-converters-own-transaction-grouping.md. What remains is the converter-side
work for brokers that fold the charge into one cash total.

## Motivation

Where a broker nets commission into a single cash total and reports no separate
charge row, the fee is invisible: the cash posting is correct but the consideration
and the expense are conflated, which matters for cost basis. The residual also lands
in `Imbalance` (0038) whenever the price and the cash total disagree.

## Design

- The converter takes the fee from what the broker reported separately -- `<COMMISSION>`
  and `<TAXES>` in OFX, `Fees & Comm` in a Schwab CSV -- and emits it as an
  `INVEXPENSE` posting in the same group as the trade, paired with its `EXPENSE`
  mirror. The consideration leg is then `total + commission`, so the two cash legs
  add back up to the total the broker reported. See
  adr/0025-netted-cash-totals-are-split-into-legs.md.
- Where a broker reports charges as their own rows on their own dates (Fidelity),
  they stay separate single-posting groups. Do not fold them into the trade group;
  they are separate cash events and grouping them would misdate them.
- Deriving the fee as `|Amount| - quantity * price` was the original design and was
  rejected -- see the measurement below. It is only the commission when the price
  and the cash total are already in the same currency and the price is exact.

## Delivered

Shared helpers in `client/lib/csv/postings.ts` (`counterLeg`, `feeLeg`,
`counterLegs`, `refPrefix`), the Fidelity CSV and JSON converters emitting income
and expense counter-legs, and the OFX parser splitting IBKR's netted `<TOTAL>`.
Then the manual test data under `local/scripts`, which had four defects that made
the 0039 report unusable as the sequencing evidence this issue asks for:

- `check-balance.py` did not weigh an option leg by its 100x contract size, which
  `balance.go` has applied since 0072. It reported IBKR option residuals about 100x
  too large, which pointed the report at options rather than at the FX bug below.
  `client/lib/csv/group-balance.test-utils.ts` had the same omission.
- `convert-ibkr.py` parsed `Amount` without stripping thousands separators, so 87
  trades were silently dropped from both the FX rate sample and the price
  conversion and kept a base-currency price against an instrument-currency cash
  total. This was the dominant IBKR residual.
- `convert-ibkr.py` dropped 44 rows outright: 20 `Withholding Tax`, 13 `Broker
  Interest Received`, 11 `Broker Interest Paid`. These are the `EXPENSE` and
  `INCOME` postings 0071 exists to report.
- `convert-ibkr.py` typed `Deposits/Withdrawals` as `CASHFLOW`, so 3,779,387 of
  external funding routed to `IMBALANCE` rather than `TRANSFER_CLEARING`.
  `convert-fidelity.py` types the same events as `JRNLFUND`.

IBKR's residual after the four: 320.97 USD across 250 trade groups, from 1.35m
before.

## Why IBKR's CSV keeps its commission in the residual

`local/masters/Lee-IBKR-CWSY.csv` has no commission column, and its `Price` is
IBKR's own intraday conversion into the account's base currency while `Amount` is
in the instrument's. Both candidate rates for the conversion were measured against
the 599 trades in that master, and neither yields a commission:

- The rate `convert-ibkr.py` estimates from `|Amount| / (quantity * multiplier *
  Price)` is solved from the trade itself on any row at or above its threshold, so
  the subtraction is zero by construction -- for 333 of 599 trades. The whole
  master leaves -318.47 USD, which is not a plausible commission bill for 383
  option trades.
- A real daily `USDGBP` close, which the price master carries, leaves 235 of 599
  derived fees negative -- a commission that pays you -- with a tail to 1901% of
  notional.

So the number is not weakly-attributed commission, it is unattributable, and it
stays in the residual where it is honest about being unexplained. This costs
nothing in the product: there is no client-side IBKR CSV converter, and the
shipped IBKR path is OFX/QFX, which reports `<COMMISSION>` on 326 of 326 trades
and is split properly. The QFX cannot backfill the CSV's history either -- the two
masters are disjoint, the CSV ending 2025-01-06 and the QFX starting 2025-01-23.

## Residuals left behind, and where they belong

- Fidelity `JRNLFUND` and `TRANSFER` single-posting groups, about 636k and 200k --
  external transfers, matched by 0068.
- Helen-Fidelity's 23 unpaired `BUYSTOCK` groups and one `SELLSTOCK` -- trade and
  cash pairing failures. Closed by 0065: neither master leaves an unpaired trade
  group now. 0069 was a different failure in the same list -- a lone `CASHFLOW`
  group per deposit, 8 in Lee and 13 in Helen -- and closing it left none of those
  either.
- Schwab's three groups worth 667,750 -- the export's quantities are split adjusted
  while its prices are as traded, so pre-split GOOG and TSLA rows cannot balance.
  That is 0057.
- Schwab has no client-side converter to update, only `local/scripts/convert-schwab.py`,
  which already splits `Fees & Comm`. Writing one is 0073.
