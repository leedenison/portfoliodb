# Price coverage is stored, and the carry-forward is derived

Prices and corporate events both need to record which date ranges a provider has
answered for. Corporate events always had a `corporate_event_coverage` table,
because an event-free range is indistinguishable from an unfetched one and
adr/0005 settled that an empty result is authoritative coverage. Prices inferred
the same thing from `range_agg` over `eod_prices` -- covered meant "rows happen
to exist here" -- and wrote a synthetic forward-filled row for every non-trading
day so that the inference came out contiguous.

The two are the same concept and now share `price_coverage`, keyed
`(instrument, plugin)` like its counterpart. Inference could not express a
negative answer: `ErrNoData` and ranges beyond a plugin's `max_history_days`
recorded nothing, so a delisted or pre-IPO range was rediscovered as a gap and
refetched on every cycle for ever.

With coverage stored, the synthetic rows lost their reason to exist and the
carry-forward moved into the valuation query, bounded by coverage. That leaves
nothing derived in the price tables: `eod_prices` holds only bars a provider
reported, `price_coverage` says which ranges were answered for, and the filled
series is computed from the two. The invariant is one-way -- every price row lies
within some span, but a span holding no rows is meaningful and is precisely what
row presence could never say.

## Considered options

**Carry the synthetic rows in the export instead of coverage declarations.**
Rejected once coverage became stored state: it is no longer derivable from rows,
so the file must carry it, and the synthetic rows are then regenerable from
(bars, coverage). Exporting them would be roughly 40% more rows for no
information.

**Keep materialising the fill as a rebuildable cache.** Deferred rather than
rejected. Measured at 860ms for 50 instruments over 5 years against 739ms for
the flat scan the stored-fill design used, and that 16% overstates it because
the comparison scans only real bars while the old design also stored a row per
weekend and holiday. If it ever becomes worth it, the cache must be a pure
function of (bars, coverage) so it can be rebuilt and checked -- which is exactly
what the synthetic rows were not, being written by the write path from the
write's own parameters.

**Infer the fill from the rows alone, with no declaration.** Rejected: it needs
an arbitrary maximum gap to bridge, and guesses wrong on the case that motivated
this -- an instrument held over two separate periods, where it would either
invent months of stale prices across the gap or refuse to bridge a legitimate
exchange closure.

**A TimescaleDB continuous aggregate.** Not applicable. Continuous aggregates
aggregate rows that exist into time buckets; they cannot invent a row for a day
with no data, which is the entire job of a carry-forward.

## Consequences

A per-`(instrument, date)` lateral is the wrong shape for the read-time fill.
`eod_prices` is a hypertable, and a lookup whose answer lies at a
data-dependent distance in the past defeats chunk exclusion. The query uses a
grid-plus-window fill instead: the covered grid joined to real bars, then the
two-window gaps-and-islands idiom, PostgreSQL having no `IGNORE NULLS`.
Partitioning by span is what bounds the carry-forward, so a delisted
instrument's last close stops at the end of its coverage rather than being held
for ever.

Coverage is per plugin rather than per instrument. Recording "nothing here"
against the instrument alone would silence every other plugin for that range,
including one configured later precisely because it reaches further back.
