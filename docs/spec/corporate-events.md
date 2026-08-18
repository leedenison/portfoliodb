# Corporate events

This document covers the design and operating model of the corporate event subsystem (stock splits and cash dividends), including the parts that are not yet implemented.

## What is stored

Two event tables in PostgreSQL, both keyed by `(instrument_id, ex_date)`:

- **`stock_splits`** — `split_from`, `split_to` (decimal NUMERIC), `data_provider`, `first_known_at`. The factor is `split_to / split_from`.
- **`cash_dividends`** — `amount` (per share), `currency`, optional `pay_date` / `record_date` / `declaration_date`, optional `frequency`, `data_provider`, `first_known_at`.

Plus two auxiliary tables:

- **`corporate_event_coverage`** — per `(instrument_id, plugin_id)`, the half-open `[covered_from, covered_before)` date intervals that have been queried successfully. Adjacent and overlapping intervals merge on insert; the merged row keeps the oldest constituent `last_fetched_at`, since a union is only as freshly confirmed as its stalest part. Coverage is the source of truth for "which date ranges have we already asked this plugin about" — see [Fetch model](#fetch-model) below.
- **`corporate_event_fetch_blocks`** — `(instrument_id, plugin_id, reason, first_blocked_at)`. A plugin returning a permanent error (404, 403, subscription limit) for an instrument lands here so the fetcher does not retry indefinitely.

The `eod_prices` and `txs` tables also gain `split_adjusted_*` columns alongside the raw OHLCV / quantity / unit_price values, so both views are debuggable side by side. See [Adjustment](#adjustment) below.

## Sources

Three sources feed `stock_splits` and `cash_dividends`. All three write through the same upsert path (`UpsertStockSplits` / `UpsertCashDividends`) and are distinguished by the `data_provider` column.

| Source | `data_provider` | Mechanism |
| --- | --- | --- |
| External market data plugins | plugin id (e.g. `"massive"`, `"eodhd"`) | Background fetcher worker (`server/corporateevents`) |
| Admin archive import | `"import"` | `ImportCorporateEvents` admin RPC |
| Broker statement parsers (planned) | `"import"` | Client-side per-broker parsers; submit through `ImportCorporateEvents` |

### Export and import

`ExportCorporateEvents` and `ImportCorporateEvents` carry the corporate event part of an admin archive: one `CorporateEventGroup` per instrument, holding that instrument's coverage and its splits and dividends together. See [archive-format.md](archive-format.md#corporate-events).

The round trip is lossless, knowledge time included. `Split` and `CashDividend` both carry `first_known_at` in both directions, and an importing event resolves its knowledge time from the event, else the envelope's `exported_at`, else the time it is stored. A stored `first_known_at` only ever moves backwards, so re-importing a split learned of years ago restores its original stamp rather than pushing it forward to import time. It is reporting knowledge only: retroactive OCC adjustment of options on the underlying keys off `ex_date`, which does not move, so a restamped split cannot re-adjust symbols that were already correct (see [Option contracts](#option-contracts) and docs/spec/bitemporality.md).

`exported_at` also stamps imported `corporate_event_coverage` spans, so an imported span records when it was actually confirmed rather than when it was imported.

Coverage is stored per (instrument, plugin), but an import records every span against the `import` sentinel, so the file carries spans merged across plugins. The per-plugin distinction cannot survive a round trip and is not written. There is one coverage set per instrument rather than one per event kind, because `corporate_event_coverage` has no event-kind dimension: a span says the provider was asked about those dates, not which kind of event it was asked about.

The broker statement parsers are deferred follow-up work. They live entirely in client converters; broker tx logs that contain SPLIT entries should be parsed by the converter for the broker's specific format and submitted via `ImportCorporateEvents`. The server-side `TX_TYPE=SPLIT` filter in `server/service/ingestion/hints.go` continues to drop SPLIT txs at ingestion; corporate events are admin-only shared data, never derived from user txs (see adr/0005-corporate-events-design.md).

## Fetch model

The corporate event fetcher worker (`server/corporateevents/worker.go`) is structurally identical to the price and inflation fetchers: it sits idle until a non-blocking signal arrives on a trigger channel, then runs one cycle. A cycle does the following per held instrument:

1. Compute the required date range. Today this is `[earliest_tx_date, today + lookahead + 1 day)` where the lookahead defaults to 30 days, so the last day fetched is `today + lookahead`. The lookahead applies to cash dividends only, letting the database hold an upcoming-dividends calendar; the split recompute ignores future-dated rows (see [Adjustment](#adjustment) and adr/0005-corporate-events-design.md).
2. Subtract `corporate_event_coverage` rows for the instrument (across all plugins) from the required range to compute missing intervals.
3. For each missing interval, walk plugins in precedence order. The first plugin to return successfully (including an empty result) records a coverage row tagged with its plugin id and stops the precedence walk for that interval. An empty result is treated as authoritative coverage (see adr/0005-corporate-events-design.md).
4. After upserting any new splits for the instrument, call `RecomputeSplitAdjustments` for that instrument so the `split_adjusted_*` columns reflect the new state.

The fetch is **per-instrument**, not bulk-by-exchange (see adr/0005-corporate-events-design.md).

### What triggers a fetch today

Only an explicit call to the `TriggerCorporateEventFetch` admin RPC, or the in-process `corporateEventTrigger` channel send made by the ingestion worker after a successful `ImportCorporateEvents` job. **There is no time-based scheduler in the current implementation.** In normal operation the fetcher only runs when an admin manually triggers it.

## Daily scheduler (planned)

The corporate event subsystem needs a periodic in-process scheduler so that newly-effective splits and freshly-announced events get picked up automatically. This section is the spec for that work; nothing in this section is implemented yet.

The scheduler exists so newly-effective splits and freshly-announced events are picked up automatically without a manual trigger (see adr/0005-corporate-events-design.md).

### Required behaviour

A goroutine started in `server/cmd/portfoliodb/main.go` alongside the existing fetcher workers. On a fixed daily cadence (suggested: 02:00 UTC), it does two things in order:

1. **Fire the corporate event trigger.** Sends on `corporateEventTrigger` non-blocking. The fetcher worker wakes up and runs one cycle. Coverage rows ensure the cycle only re-queries the trailing edge, not the whole history. With the lookahead in place, the trailing edge is at most "today's gap" — usually a one-day window.
2. **Run a blanket recompute.** Calls `database.RecomputeSplitAdjustments(ctx, "")` (empty instrument id = all instruments with at least one split). This is the mechanism that catches "an existing split's ex_date crossed today" — without this call, future-dated splits stored via the lookahead would never become effective. The blanket recompute is cheap and idempotent.

The two steps run sequentially in the same handler so the recompute always sees the result of the fetch.

### Configuration

The cadence and fire time should be configurable but with sensible defaults:

- `daily_fetch_hour_utc` — defaults to 2 (02:00 UTC).
- `daily_fetch_enabled` — defaults to true.

These belong in a top-level scheduler config, not any individual plugin's JSON (see adr/0005-corporate-events-design.md).

### Skip conditions

The scheduler should not fire when:

- No corporate event plugins are enabled (fetch would be a no-op).
- No instruments are held in the eligible asset classes (also a no-op).

Both checks already exist as early returns in the worker, so the scheduler can call the trigger unconditionally and let the worker self-suppress. But emitting a noisy "fetcher woke up and found nothing" log line every day is wasteful — the scheduler should pre-check and skip cleanly when neither condition is met.

### Testing

- Unit test: stub `time.Now`, advance one tick, assert the trigger channel received a signal and that the recompute method was called.
- Integration test in the dev container: insert a stock_split with `ex_date = today` for an instrument that has prior price/tx rows; run the scheduler tick; assert that `split_adjusted_*` columns flip from `factor=1` to the new factor.
- Integration test for the trailing-edge fetch: insert coverage spanning `[earliest_tx_date, yesterday]`; run the tick; assert that the worker queried the plugin for `[today, today]` only.

### Out of scope

- Smart "next event date" scheduling.
- Cron-style runtime configuration via the admin UI.
- Backfill on startup (the next tick catches up, since gaps are computed against `current_date`).

For why these are excluded, see adr/0005-corporate-events-design.md.

## Adjustment

The `eod_prices` and `txs` tables carry `split_adjusted_*` columns alongside the raw values. The columns are populated at insert time (defaulting to the raw counterpart via a BEFORE trigger) and recomputed by `RecomputeSplitAdjustments` whenever new splits arrive.

The adjustment factor for a row with reference date `R` and instrument `I` is a
rational, returned as a numerator and a denominator rather than as their quotient:

```
over splits where
  split.instrument_id = I
  AND split.ex_date > R
  AND split.ex_date <= current_date

num = product of split.split_to
den = product of split.split_from
```

Both products are exact, and callers multiply by `num` before dividing by `den` so
the single division comes last. That ordering is what keeps the exact part exact
for as long as possible; see adr/0028-cumulative-split-factor-is-an-exact-rational.md.

The reference date `R` is the row's **share count basis** -- the date at which the share count its raw values are denominated in was current. It is declared by whoever supplied the row and stored on the row itself, not inferred from when the row was fetched; see [bitemporality.md](bitemporality.md#share-count-basis).

The `ex_date <= current_date` clause is the future-date guard: without it a future-dated split pulled in by the lookahead would scale every prior row the moment it was fetched, even though the user still holds pre-split shares trading at pre-split prices. Future-dated splits instead sit inert until their ex_date passes, at which point the next recompute picks them up -- see [Daily scheduler](#daily-scheduler-planned) and adr/0005-corporate-events-design.md.

Because the guard is evaluated against the wall clock, the `split_adjusted_*` columns are as-of now rather than as-of any stored date. Arithmetic must never mix the two share counts: raw quantity multiplies raw price, split-adjusted quantity multiplies split-adjusted price. Mixing them across a split scales the result by the split factor.

Adjustment math. A quantity adjusts by the factor and a price by its reciprocal,
so `num` and `den` swap places between the two:

- `split_adjusted_close = close * den / num` (and same for open / high / low)
- `split_adjusted_volume = round(volume * num / den)` (more shares trade in adjusted-share terms)
- `split_adjusted_quantity = quantity * num / den` (more shares held)
- `split_adjusted_unit_price = unit_price * den / num` (per-share price drops)

The cost-basis invariant `qty × price == split_adjusted_quantity × split_adjusted_unit_price` is preserved by construction.

The quotient is exact whenever `den` divides the product -- every forward split, and every ratio whose denominator factors into 2s and 5s (2:1, 3:2, 5:4). It is not exact for a genuine reverse `/3`, so the `split_adjusted_*` columns declare a rounding scale of 12 decimal places, recorded in the schema alongside them. The rounding is confined to that derived cache: the raw columns stay exact and the adjusted pair is recomputable from them at any time. An exact check -- a group balance, a checked holding declaration -- reads the raw columns instead.

### Option contracts

A split on an option's underlying restates the option itself: OCC adjusts the contract on the ex_date, so its symbol and strike change. `ProcessPendingOptionSplits` applies that restatement retroactively to stored options. The symbol in force is closed at the ex_date and the adjusted one minted from it, so both names remain stored and a broker file exported either side of the split resolves to the same contract. The strike moves and the option's `split_adjusted_*` values are recomputed. The instrument's name is not written: it is derived from whichever identifier is still in force.

Whether an option still needs restating is decided by comparing the OCC symbol's own `valid_from` -- the point in market time that name became correct -- against the split's `ex_date`. A name that became correct on or after the ex_date already carries the adjusted symbol and is left alone; one that became correct before it does not, however long the split had been known, because providers list the pre-split symbol until the ex_date. Knowledge time is not consulted. A name with no recorded vintage falls back to the option's own first trade date, because a source names an option under the symbol current at its export and an export cannot precede the purchase it describes. See adr/0055-identifier-validity-is-an-interval.md.

A split reaches only the contracts that were listed on its effective date, so an option is restated when `expiry >= ex_date`. One expiring on the ex_date is included: OCC restates it that morning and it can still be exercised that day. One that had already expired was never restated, and it is left alone however early its name became correct. The same bound applies when an OCC hint is rebased during identification, so an expired option resolves to the symbol it expired under rather than to one that never traded. See adr/0036-expired-options-are-not-restated.md.

That comparison only works because an identification dates the name it writes from the vintage of the OCC hint it resolved from rather than from the time it ran. Transactions imported before their underlying's splits are known -- the ordinary ordering -- carry a pre-split OCC that `AdjustOCCForKnownSplits` has no split to rebase, and the provider answers about that contract, so the name is correct as of the file's export and not of the present. Options identified that way are exactly the ones this pass exists to correct once the split arrives.

A minted name's `valid_from` is the ex_date it was minted from, which is a market fact rather than a knowledge one. That is what lets a split learned of afterwards still select the option: a knowledge-time stamp would already sit after the newly learned split's ex_date and exclude it forever.

The pass derives its work list from that comparison rather than from which splits arrived in the current fetch cycle, and runs once per cycle regardless of whether any did. That makes it idempotent and self-retrying: minting closes the symbol in force in the same transaction, which is what removes the option from the list, so a restatement that failed is simply still pending next time. A future-dated split is not pending until its `ex_date` passes, at which point the next run picks it up.

One name is minted per pending split, each derived from the option's stored strike by the cumulative factor up to that split. So a backfill landing several historical splits together compounds them correctly instead of repeatedly dividing an already-divided strike, and the name the contract wore between two splits is stored rather than skipped over.

A name another instrument already holds absorbs that instrument rather than failing. It is a duplicate of the same contract, created while the split was still unknown; the option being restated survives, because it is the row carrying the contract's history.

Non-whole-forward splits (reverse splits, fractional ratios) are not applied: they are routed to `unhandled_corporate_events` for manual review, and they block their option entirely rather than being skipped over, since adjusting only the splits either side would produce a strike matching no real contract. The option stays pending so it is picked up once the event is resolved. `contract_multiplier` is never adjusted automatically.

The instruments fetched each cycle come from `HeldEventBearingInstruments`: direct STOCK and ETF holdings, plus the underlyings of held OPTION and FUTURE positions.

### Dividends

Cash dividends are stored but **not applied** to `split_adjusted_*` columns; PortfolioDB does not derive a dividend-adjusted price view (see adr/0005-corporate-events-design.md). The `cash_dividends` table is populated for calendar / reporting use.
