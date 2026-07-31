---
status: closed
title: Fidelity converter emits transaction groups
milestone: M12
dependencies: [0063]
---

Pair Fidelity's security rows with the cash rows it already supplies, and emit them
as one group. This is the tracer bullet for the upload format: the rules are measured
against the 689-row master export rather than assumed.

## Motivation

Fidelity reports the cash leg of a trade as a separate row, so deriving one from the
security row would post the money twice. The information needed to pair them exists
in the raw export and is discarded by the converters, which keep neither
`Reference Number` nor `Amount`.

## Pairing rules

Measured against `local/masters/Lee-Fidelity-CWSY.csv`:

- `Sell` -> `Cash In From Sell`: exact `|Amount|` match within (Account Number,
  Completion date). 55/55, no residual.
- `Buy` -> `Cash Out For Buy`: nearest `Reference Number` within (Account Number,
  Completion date). 36/36.
- `group_ref` is the security row's `Reference Number`.
- An unpaired cash row keeps no `group_ref`. Never drop a posting and never fail an
  upload over an unpaired leg.

## What is deliberately not grouped

- Dealing fees, PTM levies, stamp duty and FX charges stay single-posting groups.
  They are dated on the **order** date while the trade settles on the completion
  date, so folding them into the trade group would misdate them. The gap between a
  `Buy` row's `Amount` and its `Cash Out For Buy` is exactly the sum of at most three
  of those charge rows for all 36 buys, so leaving them independent accounts for
  every penny once.
- `Transfer To Cash Management Account For Fees` and `Cash In Ring-fenced For Fees`
  are the two sides of a cross-account transfer and never share an account or date.
  They are 0038's `TRANSFER_CLEARING` case and are not paired at ingest.

## Scope

`client/lib/csv/converters/fidelity-csv.ts`, `local/scripts/convert-fidelity.py` with
regenerated `local/standard-format` files, and `extension/src/brokers/fidelity-json.ts`,
whose JSON payload carries the same value as `referenceId`. The captured HAR sample
has no Buy/Sell rows, so the extension path can only be unit-tested against synthetic
rows; the CSV path carries the real-data validation.
