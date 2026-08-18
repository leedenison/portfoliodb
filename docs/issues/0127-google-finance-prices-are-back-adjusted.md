---
status: open
title: Google Finance prices import back-adjusted and are adjusted a second time
---

`cli/google` builds a price archive from GOOGLEFINANCE, which returns
split-adjusted historical closes. A bar's share count basis is fixed by
convention at its own `price_date` -- as traded -- so a bar before a split in
the imported range is stored as though it had never been adjusted, and
`RecomputeSplitAdjustments` divides it by the split factor a second time. The
result is silently wrong rather than rejected, by the factor of every split in
range: 10x for an NVDA bar before 2024-06-10, 20x for GOOG before 2022.

The command used to declare the basis per row, which is what
[0057](0057-price-share-count-basis.md) added the field for. That field was
retired by [ADR 0054](../adr/0054-share-count-basis-is-a-convention.md) on the
grounds that a source which restates knows the ratio it used and can convert
back. This source did not: it relays a third party's restatement and knows
neither the ratio nor the splits. The command warns at import for now.

Converting back needs the split history for each instrument in the sheet, which
the command cannot see -- the API exposes no splits, and GOOGLEFINANCE has no
unadjusted attribute to ask for instead. So either the API grows a way to read
the splits an instrument has and the command converts each bar before
submitting, or the GOOGLEFINANCE path is retired in favour of a price plugin
that asks its provider for unadjusted output, which is what every existing
plugin does.
