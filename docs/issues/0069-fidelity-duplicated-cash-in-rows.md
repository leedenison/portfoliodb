---
status: open
title: Investigate duplicated cash-in rows on Fidelity transfers
milestone: M12
---

A transfer arriving in a Fidelity product account is reported as two credit rows
of the same amount against a single departure, and the converter maps both to
`JRNLFUND`, so the arrival is posted twice.

## Evidence

`local/HAR/transactions-long-window.json` contains the pattern twice,
identically. A lump sum leaves the cash management account once and arrives as
two credits of the same amount. Accounts are named here by product type, and
references by their offset from the first row of the sequence:

| account                 | ref  | type                                      | dr/cr  |
| ----------------------- | ---- | ----------------------------------------- | ------ |
| Investment Fund         | n    | Transfer To Cash Management Account        | DEBIT  |
| Cash Management Account | n+3  | Transfer Into Account                      | CREDIT |
| ISA                     | n+17 | Cash In Lump Sum                           | CREDIT |
| Cash Management Account | n+19 | Transfer Out From Cash Management Account  | DEBIT  |
| ISA                     | n+20 | Cash In                                    | CREDIT |

Every row carries the same amount. The sequence recurs a year later with the
same shape and the same two duplicated credits.

Both `Cash In` and `Cash In Lump Sum` map to `TxType.JRNLFUND` in
client/lib/csv/converters/fidelity-csv.ts, so both post and the receiving account
gains twice the transferred amount.

## What to establish

- Whether the two rows are genuinely the same money. The likely reading is that
  one is the cash movement and the other is an ISA subscription record kept for
  allowance tracking, in which case only one should post. Confirm against the
  account's reported cash balance rather than inferring it from the row types.
- Whether the CSV export carries the same duplication as the JSON, since the two
  converters share the type mapping but not the source.
- Which row to keep, if one is to be dropped. `Cash In Lump Sum` carries the
  subscription semantics and `Cash In` the movement, but the reference ordering
  is the opposite of that reading and the two are not consistently ordered.

## Adjacent, probably not the same problem

`Cash In For Transfer` and `Cash Out For Buy From Transfer` appear as equal and
opposite same-account, same-day pairs, twice, in the SIPP. They net to zero so
they do not inflate a balance, but both are also mapped to `JRNLFUND` and they
should be understood before the mapping is changed.

## Why it matters here

Any inflated cash balance flows straight into money-weighted return as a spurious
external contribution. It also blocks calibration of 0068: the sample data is the
only material available for measuring a transfer-matching rule, and a transfer
whose arrival is recorded twice has no single counterpart to match against.
