---
status: open
title: Two e2e specs fail on main
---

`make e2e-test` is red on main: 27 pass, 2 fail.

- `e2e/tests/stock-split.spec.ts:43` -- "upload txs, discover split, verify
  adjustments". Fails at the option-row assertion (`toHaveCount(1)` for the row
  with quantity 1 and adjusted quantity 4); the locator resolves to 0 elements.
  The two stock-row assertions immediately above it pass, so the split is being
  discovered and stock adjustments are being applied -- it is the option leg
  that does not show an adjusted quantity.
- `e2e/tests/corporate-event-roundtrip.spec.ts:44` -- "knowledge time survives
  export and re-import". Fails in well under a second, so it is not a timeout.

## Motivation

Both were confirmed against `44d0891` (the commit that closed 0060), so they
predate the 0061 client conversion and are not caused by it. The suite being
red by default means a genuine regression in an e2e-covered path would not
stand out, and e2e is the only automated cover several client paths have.

## Design

Diagnose each on its own. For the split one, start from whether the option leg's
adjustment is missing in the database or only missing from the ListTxs response
-- the second stock assertion passing narrows it to the option path. The
knowledge-time one failing that fast suggests the import or the export is
erroring rather than returning wrong data; check the response before the
assertion.

Neither is covered by CI today: the workflow runs the four test targets plus the
checks, and `e2e-test` is not among them. Adding it is a separate decision --
the suite needs the full stack and VCR cassettes -- but the failures should be
fixed either way.
