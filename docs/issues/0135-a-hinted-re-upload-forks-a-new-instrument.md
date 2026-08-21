---
status: closed
title: A hinted re-upload forks a new instrument instead of enriching the existing one
milestone: M17
---

A description already held by a broker-description-only instrument should reach
that instrument when a later upload carries identifiers, not create a second one
beside it.

## Motivation

Path A in server/service/ingestion/resolve.go resolves by identifiers alone. A
broker-description-only instrument is findable only by its `BROKER_DESCRIPTION`
row, so `ResolveIDsByHintsDBOnly` misses it, the plugins run, and
`EnsureInstrument` is called with `storeSourceDescription` false -- no
description among the identifiers, nothing matches, and a new instrument is
created. The old one keeps the transactions that were already attached to it.

The pre-pass does look the description up, but files the hit under
`cacheKey(source, description)` while `Resolve` looks up
`cacheKeyWithHints`, which differs as soon as any hint is present. So the answer
is in the cache and is never consulted.

This is 0106's first question showing up as a live defect rather than a
hypothetical: the imports best placed to bind a description to a real identity
are precisely the ones that do not.

## Scope

adr/0004 chose not to store a `BROKER_DESCRIPTION` on this path deliberately --
the client's identifiers are authoritative and a description-derived mapping
would pollute later lookups. That reasoning does not have to be reopened to fix
the fork. Have Path A consult the description lookup as well as the identifier
lookup, and when it finds a broker-description-only instrument, attach the
plugins' identifiers to that instrument rather than minting a new one. Nothing
new is stored and the authoritative-identifiers rule stands.

Whether the mapping should also be persisted is the separate question 0106 asks,
and answering it amends adr/0004.
