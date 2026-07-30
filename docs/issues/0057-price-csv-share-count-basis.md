---
status: open
title: Carry share_count_basis through the price CSV import and export
---

Let a price CSV declare which share count its values are denominated in, so a
back-adjusted series is not adjusted a second time on import.

## Motivation

`ImportPriceRow.share_count_basis` exists and defaults to `price_date`, meaning
as-traded. Nothing on the CSV path can set it: `csvToPrices` does not read a
`share_count_basis` column, and `ExportPriceRow` has no such field, so the round
trip cannot carry it either.

A file holding a back-adjusted series therefore imports as as-traded, and
`RecomputeSplitAdjustments` divides its pre-split rows by the split factor a
second time while the matching transaction quantities are multiplied by it. The
result is silently wrong rather than rejected: a NVDA holding before 2024-06-10
values 10x low, AMZN, GOOG and GOOGL before their 2022 splits 20x, TSLA 3x.

This is not hypothetical. GOOGLEFINANCE returns split-adjusted historical
closes, so any price file built from it is back-adjusted, including the one the
project's own manual-test data was derived from.

## Design

Mirror the two conventions the format already has, so the basis can be declared
once for a whole file and overridden per row where a file mixes vintages:

- A file-level `# share_count_basis=YYYY-MM-DD` comment header, exactly as the
  standard transaction CSV already carries (see docs/spec/csv-format.md).
- The per-row `share_count_basis` column, overriding the header for that row.

Add `share_count_basis` to `ExportPriceRow` and emit both from `pricesToCsv` so
the round trip is lossless.

See docs/spec/bitemporality.md.
