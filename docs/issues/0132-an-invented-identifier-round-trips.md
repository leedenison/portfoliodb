---
status: open
title: An invented identifier round-trips before it is trusted
milestone: M17
dependencies: [0131]
---

A proposed identifier that no source stated must be checked against something
independently known before it is allowed to decide which instrument a
transaction belongs to.

## Motivation

An identifier plugin can tell us a proposal is valid. It cannot tell us it is
correct. OpenFIGI Mapping answers for any real code, so a plausible invented
ISIN -- which is usually a real ISIN belonging to a different security -- comes
back confirmed. EODHD is looser still: `pickQuery` prefers a ticker over an
ISIN, and its ISIN round-trip check only runs when the query was an ISIN, so a
proposed ticker returns another company's ISIN unverified.

The damage is not a merge; 0130 closes that path by keeping proposals out of the
identifier set. It is durability. A wrong attachment becomes a
`BROKER_DESCRIPTION` binding that is canonical, instance-global and never
re-examined -- `FindInstrumentBySourceDescription` hits on every later upload
and no plugin runs again. 0106 makes the same point about bare tickers: a silent
error rather than a failed import.

A blank is the alternative and it is the better failure. It is visible, it is
repairable (0104, 0127), and it does not propagate.

## Scope

Where a stated identifier exists, probe: resolve by the stated set, resolve by
the stated set plus the proposal, and discard the proposal when they land on
different instruments. The MIC_TICKER-versus-OPENFIGI_SHARE_CLASS check in
server/service/ingestion/resolve.go already has this shape -- two resolutions
with a nil cache under a `mismatch_check` purpose, compared by instrument id --
and generalises.

Where no stated identifier exists, there is nothing to probe against and the
test degrades to agreement: the resolved instrument must not contradict the
stated currency or the security type. Zero fields confirmed means discard, not
accept.

A proposed disambiguator needs no probe. It only re-ranks among listings the
real key already produced, so `CompareHints` scoring it is enough.

`resolution_key.mismatch_detected` becomes a text column naming which probe
disagreed. Two different findings must not share one boolean.

No similarity threshold on names. What that threshold should be is a question
for the measurement in 0105 once 0134 makes it answerable.
