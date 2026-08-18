# Broker import browser extension

A Chrome MV3 extension that automates the manual transaction import loop for a broker: it asks PortfolioDB for the most recent transaction it already holds for that broker, computes a date window, drives the broker website to export the transactions covering that window, converts and uploads them, and reports the outcome.

The first supported broker is Fidelity UK (fidelity.co.uk). The artifact is the broker's transaction-history export, not a PDF statement. Which payload that is depends on the broker: for Fidelity it is the JSON its own CSV export is generated from, which is one request rather than two and carries identifiers the CSV discards.

The rationale for uploading directly rather than driving the web UI, for the bearer-token session, and for the overlapping replace window is in (see adr/0014-extension-transaction-import.md).

## Scope

- The trigger is manual and attended: a Sync button in the extension popup. There is no scheduling and no background execution.
- The extension is a client of the existing gRPC-Web API. It introduces no server-side state of its own.
- Where the extension and the web UI consume the same payload, they share a converter rather than reimplementing parsing. Where the payloads differ, the parts that classify broker data -- the transaction type map, which types are cash, and how a row's reference becomes a correlation (see [postings.md](postings.md)) -- are still shared, so the two cannot disagree about what a broker type means or about what its identifiers are comparable by.

## Sync

A sync run performs these steps in order. Any step failing aborts the run and records the failure in the run log; no partial upload occurs.

1. **Session.** Obtain a session id (see Session below). On `UNAUTHENTICATED`, open a PortfolioDB tab to re-bootstrap and abort the run.
2. **Latest transaction.** `ListTxs` with `broker` set to the target broker, `descending` true and `page_size` 1.
3. **Window.** Compute the half-open `[from, before)` (see Date window below). If `from >= before` there is nothing to fetch: report "already up to date" and stop.
4. **Export.** Run the broker recipe (see Recipes below) to capture the payload for the window.
5. **Convert.** Run the registered converter for the broker and format.
6. **Report dropped rows.** If the converter reported errors, record them and continue (see Unparseable rows below).
7. **Guard against an empty upload.** If the conversion produced no transactions, refuse to upload (see Empty result below).
8. **Upload.** `UpsertTxs` (see Upload below).
9. **Await the job.** Poll `GetJob` to a terminal state and record the outcome, including any validation and identification errors.

## Date window

The window is:

```
before = start of day, today
from   = max(historyStartDate, latestBrokerTx - lookbackDays)
```

where `latestBrokerTx` is the timestamp from step 2, or, when the user has no transactions for this broker at all, `historyStartDate` from configuration. If there is no latest transaction and `historyStartDate` is not configured, the run fails with an explicit message; it does not guess a start date.

`before` is the start of today, so the window covers through the end of yesterday: a transaction dated today may still be incomplete at the time of export.

### Why the window overlaps

The window deliberately starts before the last known transaction rather than the day after it.

A posting is filed under its `order_date` (adr/0051-a-posting-carries-an-order-date-and-a-trade-date.md), and a broker states that date from the moment the row appears. So a row that settles between one sync and the next keeps the date the replace window is matched against, and no longer moves across a window boundary as it completes -- the duplicate this section used to describe cannot arise from settlement alone.

What remains is a broker correcting a row it has already reported: an order date restated after the fact is a valid-time correction, and a window beginning after the last known transaction would leave the previously stored row outside the delete while the re-dated one is inserted inside it. The overlap exists to absorb that. This gives the sizing rule:

> `lookbackDays` must exceed the longest plausible lag between a row first appearing and the broker settling on its final order date.

The default is 14 days. Overlap is otherwise free, because ingestion is idempotent by replacement (see adr/0002-transaction-ingestion-model.md).

### Timezone

The configured timezone (default `Europe/London`) determines only **which calendar day** is "yesterday" and which calendar day a lookback lands on.

The window bounds are then materialised as local midnights in the runtime's own timezone, because the converters construct transaction timestamps the same way. Constructing the bounds in a different timezone from the rows they are meant to bracket would shift the boundary by up to a day.

## Upload

`UpsertTxs` carries an archive transaction window -- the same message an archive
document carries, so an upload describes itself. See
[archive-format.md](archive-format.md).

| Field | Value |
| ----- | ----- |
| `window.broker` | The target broker enum. |
| `window.source` | The same source string the web UI produces for this broker and format, e.g. `Fidelity:web:fidelity-csv`. |
| `window.period_from`, `window.period_before` | The **requested** window from step 3 -- not the minimum and maximum of the parsed rows. |
| `window.postings` | The converted postings. |
| `filename` | A synthetic name identifying the run, e.g. `fidelity-ext-2026-07-27.json`. |

Two of these need care.

**The period must be the requested window.** Ingestion replaces every transaction for the user and broker within `[period_from, period_before)`. Sending the parsed row range instead would shrink the window to the transactions that still exist, so a transaction cancelled by the broker since the last sync would never be deleted -- which is the main reason to re-fetch an overlapping period at all.

**The source string is reused, not invented.** `source` is the cache key for instrument resolution. A new source string for extension uploads would miss the cache and force fresh calls to paid identification plugins for descriptions that have already been resolved. `filename` is the field that distinguishes extension runs from manual uploads in the job list.

### Share count

The extension reads the broker's live web UI, which is the one import path where a broker could present historical rows restated into post-split terms.

Quantities and unit prices are denominated in the share count current on the row's own transaction date, and no broker found so far reports anything else. A converter for a broker that does restate converts back before it emits: the broker restated by a ratio the converter can read out of the same export, so the knowledge is already where the work has to happen, and nothing downstream has to be told. See [bitemporality.md](bitemporality.md#share-count-basis).

### Account scope

The replace deletes on user, broker and period; it does not discriminate by account. This is only safe when a single export covers every account the user holds at that broker. A broker whose export is per-account must have its recipe enumerate the accounts and merge them into one upload, or the sync will delete the accounts it did not export.

### Empty result

The server short-circuits a bulk upload that contains no storable transactions: it marks the job successful without performing the replace, so nothing is deleted. An upload of zero transactions therefore does not mean "the window is now empty", it means "nothing happened".

The extension does not paper over this. If conversion yields no transactions it refuses to upload and reports that the window could not be cleared, rather than reporting a success that had no effect.

## Unparseable rows

A converter rejects rows it cannot map -- most commonly a broker transaction type absent from the converter's type map. The extension **uploads anyway** and warns prominently.

This is a deliberate trade-off. Because ingestion replaces the period wholesale, a dropped row is not merely skipped: any previously stored copy of it inside the window is deleted and not replaced. Uploading regardless keeps the sync usable when a broker introduces a new transaction type, at the cost of temporarily losing those rows until the converter is updated.

The warning must therefore be hard to miss, and the run log must record enough to fix the converter:

- The number of rows dropped.
- Each dropped row's index in the source payload, and why it was dropped.
- The distinct unrecognised transaction type strings, named explicitly. These are what get added to the converter's type map.

## Session

The extension authenticates with the same opaque session the SPA uses, carried as a bearer token rather than a cookie. A service worker request to the PortfolioDB origin is cross-site, so the `SameSite` session cookie is not attached; the API accepts `Authorization: Bearer <session_id>` as an alternative to the cookie.

Bootstrap: a content script on the PortfolioDB origin runs in the page's own context, where the session cookie does apply. It calls `AuthService/GetSession`, which returns `session.session_id` in the response body, and passes it to the service worker, which stores it and uses it for all subsequent calls.

Re-bootstrap: any call returning `UNAUTHENTICATED` invalidates the stored id and prompts the user to open a PortfolioDB tab, which re-runs the bootstrap. Sessions slide on use, so an extension in regular use does not expire.

The stored session id is a live credential held in extension storage. This is accepted for an attended, personal-use tool; it is not a pattern to extend to a shared or unattended client.

## Recipes

Everything broker-specific and site-specific is expressed as data -- a *recipe* -- executed by a generic interpreter that contains no broker knowledge. When a broker changes its markup, the repair is an edit to the recipe.

A recipe declares:

- The broker enum and the converter format id, which together produce the source string.
- The origins the extension needs access to.
- The date format and timezone the site expects for the window bounds. Broker date parameters are inclusive, so the recipe's `{{to}}` is filled with the day before the exclusive bound.
- How to obtain the payload.
- Probes: selectors that indicate whether the user is logged in and whether the export is ready.

### Selectors

Every selector is an **ordered list of candidates**; the interpreter tries each in turn and uses the first that matches. A single renamed class or id therefore degrades rather than breaks the run. Each step records which candidates were tried and which one matched, so a failure log names the exact selector that died.

### Obtaining the payload

Two strategies, in order of preference:

1. **Request replay.** The recipe declares the export request (URL, method, headers, body template) with the window bounds substituted in. The content script issues it from the page's own origin with credentials included and reads the response body as text. This covers an XHR returning either JSON or CSV as well as a plain download URL, since all are ordinary HTTP requests. It is the strategy Fidelity uses, and it needs only the session cookie.
2. **DOM driving.** The recipe declares a sequence of navigate, wait, click and set-value steps against the page, and the interpreter captures a file the page builds client-side.

Request replay is preferred because it is far less sensitive to markup changes: it depends on one URL rather than a chain of selectors.

### Dry run

The popup offers a dry run that performs every step through capture and conversion -- session, window, export, convert -- and displays the requested window, what the export returned and the conversion result, **without uploading**. This is how a recipe is developed and repaired against the live site without touching stored data.

## Configuration

Held in extension storage:

| Setting | Default | Purpose |
| ------- | ------- | ------- |
| `portfoliodbOrigin` | none | Base URL of the PortfolioDB deployment. |
| `currency` | none | Settlement currency passed to the converter, for converters that require it. |
| `historyStartDate` | none | Window start for the first sync, when no transactions exist for the broker. |
| `lookbackDays` | 14 | Overlap before the last known transaction. Must exceed the broker's order-to-completion lag. |
| `timeZone` | `Europe/London` | Determines which calendar day is "yesterday". |

## Run log

Each run appends an entry retaining: the requested window and how it was derived, each recipe step with the selector that matched and its duration, the row and transaction counts, every dropped row and the distinct unrecognised types, the upload job id, and the terminal job status with any validation and identification errors.

The log is what makes an unattended-looking failure diagnosable after the fact, and it is the source of the information needed to extend a converter's type map.
