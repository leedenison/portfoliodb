# Calendar-day valuation with TimescaleDB gapfill

Valuation and the price cache work in **calendar days** throughout, not a trading
calendar with business-day logic. Holdings are forward-filled from the last
transaction date and closing prices are forward-filled across weekends and
holidays, so a position valued at $100 on Friday shows $100 on Saturday and
Sunday rather than a gap. Calendar days give the performance chart a uniform
x-axis and avoid maintaining exchange-specific trading-calendar data; an
instrument is reported "unpriced" only when `locf()` finds no prior observation
at all, not merely because a date is a non-trading day.

Price forward-filling uses TimescaleDB's `time_bucket_gapfill('1 day', ...)` with
`locf()` directly in the valuation SQL, which produces a continuous daily series
with no application-level fill logic. Both are core TimescaleDB functions (not the
toolkit) and are available in the `timescale/timescaledb:latest-pg16` image the
project already runs, so this adds no new dependency.

Date ranges throughout the cache are half-open with midnight-UTC values so range
arithmetic composes cleanly with the database (see
adr/0018-half-open-date-intervals.md).
