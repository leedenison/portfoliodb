---
status: closed
title: Candidate plugins complete a partial identity
milestone: M17
dependencies: [0130]
---

Generalise the description stage from extracting identifiers out of free text to
completing whatever the source left out, and rename it to match.

## Motivation

The OpenAI description plugin is given a broker description and a security type
hint, and returns one identifier: an OCC symbol for an option, or a
`MIC_TICKER` with an empty domain for everything else. It never sees the
currency the transaction states, never sees the identifiers the source supplied,
and never returns an exchange.

That fits one of the four shapes an upload arrives in. A source may identify the
instrument outright; or leave out something that follows from what it gave
(ticker and exchange but no currency), which an identifier plugin fills; or
leave out something where several answers are genuinely valid (a ticker with no
venue); or give free text alone. Only the last two need this stage, and the
third is the common one the current design cannot serve at all -- Path A in
server/service/ingestion/resolve.go skips description plugins entirely whenever
the client supplied identifier hints, which is exactly when a partial identity
arrives. An IBKR QFX carries a CUSIP, a currency and a ticker, and no venue,
and never reaches the plugin.

## Scope

The stage takes everything known -- description, ISIN, CUSIP, ticker, exchange,
currency, security type -- and proposes what is missing, under the rules in
0130. It runs when the stated identity is incomplete rather than when it is
absent, and only after both DB lookups have missed, so a description already
resolved is never paid for a second time.

Complete means: cash, which never goes to a paid plugin; a self-describing
derivative symbol; or a ticker qualified by its venue, with or without a
currency. An ISIN or CUSIP alone is incomplete, and so is a bare ticker.

Rename the stage to `candidate`. The contract inverts -- input stops being free
text and output stops being what was extracted -- and a reader of
`ExtractBatch` will keep writing extraction-shaped code against it. The spec
already says "candidate identifiers" in this role, so it costs no new
vocabulary, and it avoids colliding with OpenAI's own "completion", which
telemetry already uses for a token column. The rename reaches the plugin
category in `plugin_config`, the proto enum and its two admin RPCs, the
telemetry table and view, the admin page and the Grafana panels.

Archive imports are excluded: they restore identities they already carry.

## Rejected

Showing the model an enumerated candidate list -- the listings a first
identifier-plugin pass returned -- and asking it to choose. It makes the output
valid by construction, so the validity check afterwards stops carrying any
information. OpenFIGI mapping rows carry no currency and no country either; for
one US stock they differ only in a two-letter exchange code, so there is little
to match on and the model would be recalling venue codes anyway, but now with a
guarantee of returning something that passes.
