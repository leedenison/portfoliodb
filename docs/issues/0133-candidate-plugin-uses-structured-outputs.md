---
status: closed
title: The candidate plugin uses structured outputs and returns per-field confidence
milestone: M17
dependencies: [0131]
---

Ask the model for a schema-checked object naming each field it is completing,
rather than parsing a JSON blob out of a chat response.

## Motivation

server/plugins/openai/candidate/client.go builds a prompt asking for one of two
keys, strips markdown fences off the reply and unmarshals what is left. A
tolerant parser hides a schema regression, and the prompt cannot express "these
fields are known, complete the rest" at all. One HTTP error kills the whole
chunk with no retry.

The integration test asserts only which identifier type came back, never the
value, so a confidently wrong ticker passes -- 0105's complaint.

## Scope

Use `response_format` with a strict JSON schema and delete the fence-stripping
rather than keeping it as a fallback. Send only the fields that are known, so
the model is not primed to echo blanks, and take `null` for a field it will not
guess. Exchange as an ISO 10383 operating MIC, currency as ISO 4217. Keep
temperature at zero and add a seed so the measurement reproduces. Retry once
with backoff on 429 and 5xx: the orchestrator cannot retry a batch on the
plugin's behalf.

Return a per-field confidence, record it, and do not gate on it. An LLM's
self-report is uncalibrated, and wiring it into control flow now would invent a
threshold with no evidence behind it. Its value is as evidence: joined against
the per-field outcome in 0134 it either correlates with correctness or it does
not, and that is what would justify a threshold later. Say so in the doc
comment so nobody wires it in. Control flow gates on the round-trip in 0132.

New cassettes, and the integration test asserts values -- an LSE ETF, a NYSE
stock, a NASDAQ stock, an option, and an unlisted fund that must come back with
every field null.
