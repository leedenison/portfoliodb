---
status: open
title: Client-side Schwab transaction converter
dependencies: [0040]
---

Add a Schwab CSV converter to `client/lib/csv/converters/`, so a Schwab export can
be uploaded through the web client as Fidelity and IBKR already can.

## Motivation

`SCHB` is in the `Broker` enum but has no converter, so a Schwab user has only the
standard format to go through -- which means converting by hand. Schwab is also the
third shape of the fee problem: it nets commission into `Amount` while reporting it
separately in `Fees & Comm`, so under
adr/0025-netted-cash-totals-are-split-into-legs.md its converter splits the total
into a consideration leg and a fee leg. That is the case 0040 covered for IBKR's
OFX and Fidelity's own rows, and left for Schwab only because no converter existed
to update.

## Design

- Consideration is `Amount + fee`, not `quantity * unit_price`. The broker's own
  arithmetic is what the two cash legs have to add back up to, and here the export
  makes the point twice over: its quantities are split adjusted while its prices
  are as traded, so the product is wrong on any row predating a split.
- The fee leg comes from `feeLeg` in `client/lib/csv/postings.ts`, and its expense
  mirror from `counterLegs`, called last so a derived fee gets one too.
- `local/scripts/convert-schwab.py` is a working reference for the `Action` to
  `TxType` map and for the group and cash-leg construction. It is not a
  translation target: it reads a CWSY-preprocessed master, and the converter has
  to read what Schwab actually exports.

## Blocked on sample data

No genuine Schwab export is in the repo. `local/masters/Helen-Schwab-CWSY.csv` has
been through CWSY pre-processing: columns 0-7 are the raw Schwab view and 8-13 are
added. Writing the converter against those first eight columns would leave every
real-export quirk unverified -- the preamble and total rows, `"02/08/2022 as of
02/07/2022"` dates, quoted dollar amounts, the account line. Get a real export
first.

## Out of scope

The mixed share count basis noted above is 0057, not this issue. It is what leaves
three groups in `Helen-Schwab.csv` unbalanced by 667,750 USD, and no fee handling
will close that gap.
