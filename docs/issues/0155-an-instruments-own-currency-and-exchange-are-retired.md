---
status: closed
title: An instrument's own currency and exchange are retired
milestone: M25
dependencies: [0151, 0152, 0153, 0154]
---

The last place the old grain survives.

## Scope

Drop `instruments.currency`, `instruments.exchange_mic` and the `exchange`
denormalisation derived from it; `recompute_instrument_name` stops computing
`exchange` and keeps computing `name`, which is a security fact.
`Instrument.exchange_info` becomes per-listing, populated by joining
`listing_venues` to `exchanges`.

Mechanical, and expected to run past the PR size guidance for that reason.
Closes out what 0099 asked for: the foreign key to `exchanges`, the single-table
exchange filter in `ListInstruments` and the `exchange_info` join all survive on
`listing_venues`, and divergence between the column and the identifier domain is
no longer representable because there is no column.

## Outcome

Done in two PRs. The first moved the wire surface to the line while the columns
still stood; the second removed them.

`Instrument.exchange_info` became `Listing.venues`, which changed from
`repeated string` to `repeated Exchange` rather than gaining a second list beside
it. One field, so the venue set and its reference data cannot disagree about how
many venues there are. `Instrument` lost `exchange`, `currency` and
`exchange_info` and renumbered. `PriceGap.exchange` became `repeated venues`,
read off the line the gap is a question about.

The exchange filter was on `ListInstrumentsForExport` rather than on
`ListInstruments`, and it now selects the securities any of whose lines is
admitted to the MIC. **That changes what it selects**: a security whose venue was
only ever a column and never a `MIC_TICKER` domain records no venue and is no
longer matched. Correct under this model -- there is no venue recorded for it to
be selected on -- but it is a behaviour change rather than a translation, and the
test that covered the filter had to be re-founded on `MIC_TICKER` identifiers to
mean anything.

`recompute_instrument_name` kept its trigger on `instruments` rather than losing
it with the `UPDATE OF exchange_mic` clause it carried. Fixtures insert bare
instrument rows with no identifiers, and the trigger is what gives those the
`id::text` fallback the telemetry label view promises. `AFTER INSERT` alone
cannot re-enter it, the trigger's own write being an `UPDATE`.

## Venue semantics

The retirement forced a decision the tree had never recorded, and had answered
inconsistently. Venues are an **open set**: what we have been told about, not what
exists. adr/0077 records that, and the distinction that decides how any one
comparison reads -- permissive when asking where a line is quoted, strict when
asking whether two answers describe one line. Every venue comparison now says in
a comment which reading it takes and why.

The consequence inside this issue was that `CompareHints` lost its Exchange
branch outright. It already treated the hint side as a set while treating ours as
a scalar, and under the rule there is nothing left for it to find: a stated venue
either is among the ones we hold, which is agreement, or is not, which is news.
Currency carries that check instead, and became a family-membership test over the
lines an identifier reaches -- `FindInstrumentWithMetaByIdentifier` now answers
with a security's listing currencies for a security-grain name and one line's for
a listing-grain one, and answers with no venue at all.

`resultMatchesHints` kept a strict venue test, added explicitly rather than
inherited from `CompareHints`. It ranks one answer against another, which is the
other side of the rule, and without it a proposal naming one venue would promote a
result about a different line.

## Two things worth knowing

`instruments.currency` had a live reader nothing obvious pointed at:
`balanceInstruments` in `server/service/ingestion/balance.go` turned it into the
`cur:USD` commodity that makes a cash leg cancel against the security leg it
settles. Deleting the field compiles and passes most tests, and every group in
every upload then grows a `TRANSFER_CLEARING` residual nobody asked for. It reads
the sole line now, and a test names that failure mode.

`MergeInstrumentFromArchive` no longer mints a line. Its currency was the only
thing that let it, and `EnsureArchiveInstrument` has already ensured every line
the file states by the time it runs, so a line it could mint would be one the file
did not name.

`contradicts` treated two venues as two lines, where adr/0068 says two venues
quoting one currency are one listing. That was left standing here and is now
closed as issue 0159.
