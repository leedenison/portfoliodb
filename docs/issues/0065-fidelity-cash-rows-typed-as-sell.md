---
status: open
title: Fidelity cash rows typed as Sell become bogus security transactions
milestone: M12
---

The client Fidelity converter turns 13 rows in the sample export into `SELLSTOCK`
transactions against an instrument described as "Cash".

## The problem

Fidelity labels some pure cash movements with `Transaction type = Sell`. They are
identifiable: `Action = Cash`, `Type = CASH`, and `Investments = Cash`. The client
converter maps on `Transaction type` alone
(`FIDELITY_TYPE_TO_OFX` in client/lib/csv/converters/fidelity-csv.ts), so each one
becomes a negative-quantity `SELLSTOCK` whose instrument description is "Cash" --
a security position that does not exist, sold in units of money.

`local/scripts/convert-fidelity.py` does not have the bug: it keys off the `Action`
column first and drops these rows. The two converters therefore disagree about which
rows survive, which is its own problem -- the script is meant to produce the same
standard CSV the client would.

## Design

Classify on `Action` / `Type` rather than `Transaction type` alone, in both
converters, and treat a `Sell` row whose instrument is cash as the cash movement it
is. The extension's JSON payload carries neither column, so check which of its fields
distinguishes the same rows before assuming the fix carries over.

Found while adding transaction grouping (0064). It predates that work: grouping
neither causes it nor makes it worse.
