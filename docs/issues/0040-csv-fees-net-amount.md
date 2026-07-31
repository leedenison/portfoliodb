---
status: open
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
work for brokers that do not report charges separately.

## Motivation

Where a broker nets commission into a single cash total and reports no separate
charge row, the fee is invisible: the cash posting is correct but the consideration
and the expense are conflated, which matters for cost basis. The residual also lands
in `Imbalance` (0038) whenever the price and the cash total disagree.

## Design

- The converter derives the fee as `|Amount| - quantity * price` and emits it as an
  `INVEXPENSE` posting in the same group as the trade.
- Where a broker reports charges as their own rows on their own dates (Fidelity),
  they stay separate single-posting groups. Do not fold them into the trade group;
  they are separate cash events and grouping them would misdate them.
- The currency qualifier matters. Derive the fee only after the price and the cash
  total are in the same currency.

## Sequencing

Prioritise converter updates using the imbalance report from 0039 -- fix the
broker with the largest residual first. The Fidelity converter (0013) is the
existing worked example.

## Manual test data

The broker CSVs under `local/standard-format` carry no fee postings, so every
conflated fee shows up as `Imbalance` until they are regenerated. Doing that is part
of this work: the scripts that produce them live in `local/scripts` and have to emit
the fee postings alongside the client converters.

Whether the source data supports it varies by broker, so check before assuming a
regeneration is mechanical. The IBKR master (`local/masters/Lee-IBKR-CWSY.csv`)
has no commission column at all -- only `Amount`, the cash total with commission
already in it. Two ways round that:

- Take fees from the OFX/QFX export of the same account, which does carry
  `<COMMISSION>` per transaction alongside `UNITPRICE` and `TOTAL`.
- Derive them as `|Amount| - quantity * multiplier * unit_price`, which is what
  the residual is once the price is in the instrument's currency.

That currency qualifier matters. The IBKR export quotes `Price` in the account's
base currency while `Amount` is in the instrument's, so the conversion has to
happen before any fee is derived from the difference. `convert-ibkr.py` already
does it and documents why.
