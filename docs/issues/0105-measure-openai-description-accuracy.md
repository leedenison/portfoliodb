---
status: open
title: Measure the OpenAI description plugin's accuracy and decide whether to keep it
milestone: M17
---

Measure how often the OpenAI description plugin identifies the right instrument,
against ground truth taken from broker files that carry an identifier, and decide
whether the plugin is worth its cost.

## Motivation

The plugin is a paid, per-upload call on the hot path of ingestion, and nothing
measures whether its answers are right. The integration test at
server/plugins/openai/description/integration_test.go asserts only the identifier
*type* each id came back as -- `MIC_TICKER` or `OCC` -- and never the value, so a
confidently wrong ticker passes. There is no accuracy harness, no golden set and no
scoring code anywhere in the repository.

The plugin returns no confidence, so there is no threshold to tune: the outcome is
keep, drop, or narrow it to the cases it wins.

Ground truth is now available. The extension reads the Fidelity JSON, which pairs a
description with an ISIN on the same row, and the IBKR QFX exports pair a security
name with a `UNIQUEID`. Both are the same broker descriptions the plugin is asked to
interpret, with the answer attached.

## What is being measured

The plugin does not return an ISIN. server/plugins/openai/description/plugin.go emits
`OCC` for an option and `MIC_TICKER` for everything else, with an empty `domain` and
so no exchange -- the prompt in client.go asks for exactly those two. A ground truth
of ISINs therefore cannot be compared to plugin output directly.

Measure end to end instead: does description -> extracted ticker -> identifier
plugins -> instrument land on the same instrument the known ISIN or CUSIP resolves
to. Comparing the intermediate ticker alone would need the exchange the plugin never
returns, which is the ambiguity 0106 is about rather than a property of the plugin.

## Scope

**Report the reached population, not just the hit rate.** `extractDescHints` in
server/service/ingestion/worker.go extracts a description only when it misses
`FindInstrumentBySourceDescription` and at least one posting sharing that description
arrived with no identifier hints. Path A in server/service/ingestion/resolve.go then
bypasses description plugins entirely whenever the converter supplied hints. Since
0107, client/lib/csv/converters/fidelity-csv.ts supplies a `MIC_TICKER` hint for every
security whose description ends in a ticker, so what reaches the plugin on that path
is roughly the unlisted funds -- the rows with no ticker to take. A plugin that is
accurate over a dozen rows is a different proposition from one carrying the whole CSV
path, and the decision needs both numbers.

**The cheap baseline is already in the tree, and the plugin only sees what it
missed.** `tickerOf` in the Fidelity converter takes the ticker from the trailing
parenthetical of a description, anchored to the end so that `ISHARES PHYSICAL GOLD ETC
USD (GBP) ACC (SGLN)` yields `SGLN` and not `GBP`. IBKR's `SECNAME` leads with the
ticker instead -- `AMD ADVANCED MICRO DEVICES` -- so the equivalent baseline there is
a leading token. Where such a rule fires, the converter has already hinted and the
plugin is never called; so on live data the plugin's population is precisely the
residue the free rule could not take, and a measurement must not credit it with the
rows the rule already got. Run the baselines over the whole ground-truth set to size
that residue, then score the plugin on the residue alone.

That residue is also the hardest case. An unlisted fund is named `M&G European Index
Tracker`, with no symbol anywhere in it, so the plugin is being asked for the case
where there is nothing to extract and something has to be known. Report it as its own
bucket rather than blended into one hit rate. The token columns on
`telemetry.description_plugin_call` (`prompt_tokens`, `completion_tokens`,
`total_tokens`) make the price per resolution measurable, so state accuracy and cost
together.

**Ground truth sources.** The Fidelity JSON export gives the cleanest pairing:
extension/src/brokers/fidelity-json.ts reads `assetName` alongside `isin` and `sedol`
from one row, and its `isValidIsin` check-digit helper is what separates real ISINs
from the cash pseudo-ISINs Fidelity emits. The IBKR QFX exports pair `SECNAME` with a
`UNIQUEID` and `UNIQUEIDTYPE` (client/lib/ofx/parser.ts); in practice these skew
heavily to CUSIP and CONID with only a handful of true ISINs, so IBKR mainly
contributes description-to-CUSIP pairs, plus description-to-OCC through the SECLIST
ticker path in client/lib/csv/converters/ibkr-ofx.ts.

## Deliverable

A one-off measurement, not a committed harness. Build the pair set and the comparison
under `local/`, run it once, and record here the reached-population count, the
accuracy against the baseline, the cost per resolution and the decision that follows.
If the plugin is kept, a repeatable harness can be its own issue.

The broker exports the pairs come from carry a real account number, in their contents
and in their filenames, and stay in `local/`. Nothing derived from them is committed
and no account or filename is named. Descriptions, tickers, ISINs and CUSIPs are
reference data and are fine to record.
