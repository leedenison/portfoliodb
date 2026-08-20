---
status: open
title: Measure the OpenAI description plugin's accuracy and decide whether to keep it
milestone: M17
dependencies: [0134]
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

Two numbers, and they answer different questions.

Per field: 0134 records every field the plugin proposed against the instrument
that was eventually resolved, so accuracy by field -- ticker, exchange, currency,
and any key it proposed -- is a query over that table rather than a harness. That
is the number that says whether to keep the plugin, drop it, or narrow it to the
fields it wins.

End to end: does description plus whatever the source stated land on the same
instrument the known ISIN or CUSIP resolves to. This is the number that survives
the plugin being replaced, and it is what a ground truth of ISINs can be compared
against directly.

Report both against the deterministic baseline established by 0129, not against
the state of the tree before it. Three defects there produced blank and wrong
exchanges with no plugin involved, and a measurement taken across that fix would
credit the AI with what the fix did.

## Scope

**Report the reached population, not just the hit rate.** The population moves
with 0131: the stage stops being skipped whenever the converter supplied hints and
starts running whenever the stated identity is incomplete, which is most of the
QFX path. So the plugin is being asked a harder and much larger question than it
was, and a hit rate quoted without the population behind it is not comparable to
anything. The candidate outcome on each resolution key says which keys reached the
stage and why the rest did not, so the denominator is recoverable.

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

Record here the reached-population count, the per-field and end-to-end accuracy
against the 0129 baseline, the cost per resolution, and the decision that follows.
The per-field half is a query over the view 0134 adds and needs no harness; the
end-to-end half needs the ground-truth pair set built under `local/` and run once.

The broker exports the pairs come from carry a real account number, in their contents
and in their filenames, and stay in `local/`. Nothing derived from them is committed
and no account or filename is named. Descriptions, tickers, ISINs and CUSIPs are
reference data and are fine to record.
