---
status: closed
title: Move price import and export to the archive schema
dependencies: [0078]
---

Replace the price CSV with the archive schema on both the import and the export
side.

## Motivation

The price CSV is the format that shows the flat-file problem most clearly.
`prices-recovered.csv` opens with ninety `# coverage=` comment lines, every one
instrument-scoped, because coverage diverges exactly when instruments have
different lifetimes -- option contract windows, a delisting, an IPO. The
file-wide slot the format was designed around is unused.

The same file cannot say which share count its values are denominated in without
a file-level comment and a per-row override, which is 0057, and it cannot mix
vintages at all, which is why `prices-unadjusted-broken.csv` is a separate file.

## Design

Prices become an admin archive part: instrument groups carrying coverage,
`share_count_basis`, asset class and currency, with price rows nested under
them. See adr/0035-archive-nests-by-aggregate-root.md.

Retire, rather than keep alongside:

- `pricesToCsv` and `csvToPrices` in client/lib/csv/prices.ts and their tests.
- The `# coverage=` and `# exported_at=` comment syntax, and the four
  global-versus-specific override rules that go with it.
- The price CSV section of docs/spec/csv-format.md.

`ExportPriceRow` and `ImportPriceRow` collapse into one archive message.
`exported_at` moves to the file envelope rather than being stamped onto every
row.

## Downstream

`local/scripts/recover-prices.py` and `local/scripts/convert-google-prices.py`
write the CSV today and need to emit the archive format instead. The recovery
rules in `recover-prices.py` are unaffected -- only the output stage changes,
and it gets simpler: per-instrument coverage stops being a hand-assembled
comment block and becomes the group it always was.
