# Corporate events

This document covers the design and operating model of the corporate event subsystem (stock splits and cash dividends), including the parts that are not yet implemented.

## What is stored

Two event tables in PostgreSQL, both keyed by `(instrument_id, ex_date)`:

- **`stock_splits`** — `split_from`, `split_to` (decimal NUMERIC), `data_provider`, `fetched_at`. The factor is `split_to / split_from`.
- **`cash_dividends`** — `amount` (per share), `currency`, optional `pay_date` / `record_date` / `declaration_date`, optional `frequency`, `data_provider`, `fetched_at`.

Plus two auxiliary tables:

- **`corporate_event_coverage`** — per `(instrument_id, plugin_id)`, the closed date intervals that have been queried successfully. Adjacent and overlapping intervals merge on insert. Coverage is the source of truth for "which date ranges have we already asked this plugin about" — see [Fetch model](#fetch-model) below.
- **`corporate_event_fetch_blocks`** — `(instrument_id, plugin_id, reason)`. A plugin returning a permanent error (404, 403, subscription limit) for an instrument lands here so the fetcher does not retry indefinitely.

The `eod_prices` and `txs` tables also gain `split_adjusted_*` columns alongside the raw OHLCV / quantity / unit_price values, so both views are debuggable side by side. See [Adjustment](#adjustment) below.

## Sources

Three sources feed `stock_splits` and `cash_dividends`. All three write through the same upsert path (`UpsertStockSplits` / `UpsertCashDividends`) and are distinguished by the `data_provider` column.

| Source | `data_provider` | Mechanism |
| --- | --- | --- |
| External market data plugins | plugin id (e.g. `"massive"`, `"eodhd"`) | Background fetcher worker (`server/corporateevents`) |
| Admin CSV / JSON import | `"import"` | `ImportCorporateEvents` admin RPC |
| Broker statement parsers (planned) | `"import"` | Client-side per-broker parsers; submit through `ImportCorporateEvents` |

The broker statement parsers are deferred follow-up work. They live entirely in client converters; broker tx logs that contain SPLIT entries should be parsed by the converter for the broker's specific format and submitted via `ImportCorporateEvents`. The server-side `TX_TYPE=SPLIT` filter in `server/service/ingestion/hints.go` continues to drop SPLIT txs at ingestion — corporate events are admin-only shared data and are never derived from user txs at ingestion time.

## Fetch model

The corporate event fetcher worker (`server/corporateevents/worker.go`) is structurally identical to the price and inflation fetchers: it sits idle until a non-blocking signal arrives on a trigger channel, then runs one cycle. A cycle does the following per held instrument:

1. Compute the required date range. Today this is `[earliest_tx_date, today + lookahead]` where the lookahead defaults to 30 days. **The lookahead exists for cash dividends only** — it lets the database hold an upcoming-dividends calendar pulled from provider responses. For stock splits the recompute pass ignores any future-dated rows (see [Adjustment](#adjustment)), so storing them early is harmless.
2. Subtract `corporate_event_coverage` rows for the instrument (across all plugins) from the required range to compute missing intervals.
3. For each missing interval, walk plugins in precedence order. The first plugin to return successfully (including an empty result) records a coverage row tagged with its plugin id and stops the precedence walk for that interval. Empty is treated as authoritative because the normal answer for most ticker/date windows is "nothing happened".
4. After upserting any new splits for the instrument, call `RecomputeSplitAdjustments` for that instrument so the `split_adjusted_*` columns reflect the new state.

The fetch is **per-instrument**, not bulk-by-exchange. Both Massive and EODHD support per-symbol filtering for splits and dividends, so a per-ticker loop is the natural fit and the API surface across providers is symmetric.

### What triggers a fetch today

Only an explicit call to the `TriggerCorporateEventFetch` admin RPC, or the in-process `corporateEventTrigger` channel send made by the ingestion worker after a successful `ImportCorporateEvents` job. **There is no time-based scheduler in the current implementation.** In normal operation the fetcher only runs when an admin manually triggers it.

## Daily scheduler (planned)

The corporate event subsystem needs a periodic in-process scheduler so that newly-effective splits and freshly-announced events get picked up automatically. This section is the spec for that work; nothing in this section is implemented yet.

### Why it's needed

Two distinct problems show up without a scheduler:

1. **Newly-announced events.** Providers publish a split or dividend the moment it is announced, often weeks before the ex_date. A user who imports a portfolio in January and never triggers a manual fetch will not see the AAPL split announced in May until they remember to call the trigger RPC.
2. **Newly-effective splits.** Even with the future-date guard in `split_factor_at`, a future-dated split sitting in `stock_splits` does not produce any `split_adjusted_*` change until its ex_date passes. Today, nothing in the codebase fires on the day an ex_date crosses. The recompute is only invoked when new splits are upserted; if the split landed in the database 30 days ago via the lookahead, no upsert happens on the actual ex_date and the recompute never runs.

A daily scheduler that fires the trigger channel once a day, plus a recompute pass after the fetch cycle, fixes both.

### Required behaviour

A goroutine started in `server/cmd/portfoliodb/main.go` alongside the existing fetcher workers. On a fixed daily cadence (suggested: 02:00 UTC), it does two things in order:

1. **Fire the corporate event trigger.** Sends on `corporateEventTrigger` non-blocking. The fetcher worker wakes up and runs one cycle. Coverage rows ensure the cycle only re-queries the trailing edge, not the whole history. With the lookahead in place, the trailing edge is at most "today's gap" — usually a one-day window.
2. **Run a blanket recompute.** Calls `database.RecomputeSplitAdjustments(ctx, "")` (empty instrument id = all instruments with at least one split). This is the mechanism that catches "an existing split's ex_date crossed today" — without this call, future-dated splits stored via the lookahead would never become effective. The blanket recompute is cheap and idempotent.

The two steps run sequentially in the same handler so the recompute always sees the result of the fetch.

### Configuration

The cadence and fire time should be configurable but with sensible defaults:

- `daily_fetch_hour_utc` — defaults to 2 (02:00 UTC).
- `daily_fetch_enabled` — defaults to true.

These belong in the corporate event plugin config or a top-level scheduler config — not in any individual plugin's JSON. The simplest place is a small new section of the existing config table or a hard-coded constant pair in `server/corporateevents` until we have a strong reason to expose them via the admin UI.

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

- Smart "next event date" scheduling (only fire when the calendar says we should expect something). The simple fixed-cadence model is sufficient for now and the calendar-driven model can replace it later without changing the data model.
- Cron-style runtime configuration via the admin UI. Hard-coded daily is fine for v1.
- Backfill on startup. The scheduler runs on its cadence; if the server has been down for a day, the next tick catches up because gaps and missing recompute opportunities are computed against `current_date` regardless of how long it has been since the last run.

## Adjustment

The `eod_prices` and `txs` tables carry `split_adjusted_*` columns alongside the raw values. The columns are populated at insert time (defaulting to the raw counterpart via a BEFORE trigger) and recomputed by `RecomputeSplitAdjustments` whenever new splits arrive.

The adjustment factor for a row with reference date `R` and instrument `I` is:

```
factor = product over splits where
  split.instrument_id = I
  AND split.ex_date > R
  AND split.ex_date <= current_date
  of (split.split_to / split.split_from)
```

The reference date is `fetched_at::date` for prices and `timestamp::date` for txs. The `ex_date <= current_date` clause is the future-date guard described in [Why it's needed](#why-its-needed) above; without it a future-dated split would scale rows immediately on fetch.

Adjustment math:

- `split_adjusted_close = close / factor` (and same for open / high / low)
- `split_adjusted_volume = round(volume * factor)` (more shares trade in adjusted-share terms)
- `split_adjusted_quantity = quantity * factor` (more shares held)
- `split_adjusted_unit_price = unit_price / factor` (per-share price drops)

The cost-basis invariant `qty × price == split_adjusted_quantity × split_adjusted_unit_price` is preserved by construction.

### Scope: STOCK and ETF only

The current adjustment pass only applies to instruments with `asset_class IN ('STOCK', 'ETF')`. Options need underlying-driven adjustment of strike, contract count, and per-contract premium — that's a separate planned follow-up. The `HeldStockEtfInstruments` query also filters to STOCK and ETF; underlyings of held options are not currently fetched. See the follow-up issue documents for the planned extension.

### Dividends

Cash dividends are stored but **not applied** to `split_adjusted_*` columns. The user-facing semantics of "what would I get by selling this position right now" are well-served by raw close prices, and broker-imported INCOME / REINVEST txs already capture the cash side of dividend payments, so PortfolioDB does not derive a dividend-adjusted price view today. The `cash_dividends` table is populated for calendar / reporting use.
