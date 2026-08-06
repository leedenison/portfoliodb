---
status: open
title: Carry share_count_basis through price import and export
---

Let a price file declare which share count its values are denominated in, so a
back-adjusted series is not adjusted a second time on import.

## Motivation

`ImportPriceRow.share_count_basis` exists and defaults to `price_date`, meaning
as-traded. Nothing on the file path can set it: `csvToPrices` does not read a
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

The basis belongs on the **instrument group**, alongside coverage, not on the
file. A back-adjusted series is adjusted as of a date for a given instrument, so
one file-level value cannot describe a file whose instruments were restated at
different times -- which is why `prices-unadjusted-broken.csv` is a separate file
from `prices-recovered.csv` rather than rows within it. Stating the basis per
group lets one file carry both vintages, and removes the precedence rule that a
file-level default with a per-row override would otherwise need. See
adr/0035-archive-nests-by-aggregate-root.md.

Add it to the export as well as the import, so the round trip is lossless.

## Sequencing

The bug is live now, and the price file format is moving to the archive schema
under 0081. Fixing it in the CSV first means a file-level `# share_count_basis=`
comment header plus a per-row override column -- exactly the precedence rule the
archive removes, and thrown away when 0081 lands.

Whether that interim is worth building depends on how far off 0081 is. It is a
sequencing decision, deliberately left open here; the correctness problem is the
same either way.

See docs/spec/bitemporality.md.
