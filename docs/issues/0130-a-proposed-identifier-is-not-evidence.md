---
status: open
title: A proposed identifier is distinguishable from a stated one
milestone: M17
dependencies: [0129]
---

Resolution must be able to tell an identifier a source stated from one a plugin
proposed, and must never let the second stand in for the first.

## Motivation

Nothing in `identifier.Identifier` says where a value came from, and every
consumer treats hints as authoritative. That is defensible while every hint is
stated by a source. It stops being defensible the moment a plugin can propose
one (0131), because the machinery that is supposed to test a proposal would
instead be validated by it: `resultMatchesHints` in
server/service/identification/resolve.go promotes whichever plugin agrees with
the hints, so a hallucinated hint would elect the plugin that shares it.

Two paths are worse than that. `ResolveWithPlugins` returns `Identified: true`
on a single `ResolveByHintsDBOnly` match before calling any plugin, so a
proposal reaching that path resolves with no verification at all. And ingestion
`Resolve` fails the whole job when hints resolve to more than one instrument, so
a proposal could turn a good import into a failed one.

## Scope

Carry provenance in the shape of the call rather than as a field on
`identifier.Identifier`. That type is also what becomes `db.IdentifierInput` and
gets stored, so a flag on it fails open: every producer and every store site has
to remember to clear it, and one missed site persists a hallucination as
canonical identity.

Three rules, none of which a plugin can opt out of:

A proposal never short-circuits the DB lookup and never raises
`conflicting_hints`. Both take stated identifiers only.

A proposal is never echoed into returned identifiers. This is also what keeps a
proposal out of `EnsureInstrument`'s identifier set, and so out of the eager
merge in adr/0004: a merge is driven by the identifiers passed in, so a value
that cannot enter that set cannot cause one. It closes an existing leak too --
OpenFIGI appends a matched `MIC_TICKER` hint when `assertsExchange` passes, and
that returns true when the result's own exchange is unknown, which 0129 shows is
the ordinary US outcome rather than the rare one.

Winner selection gains a tier: agreement with a stated identifier beats
agreement with a proposed one, which beats precedence. A proposal can break a
tie among plugins that all succeeded; it can never outrank something a source
said, and a contradicted proposal drops a result from the proposed tier only.

**Barring the echo does not blind us.** OpenFIGI Mapping returns zero results
when the supplied identifier did not match, so a non-empty response is itself
the confirmation that the identifier was good -- which is why the plugin can
decline to append an ISIN or CUSIP and still have told us. The non-echo is
deliberate and for an unrelated reason, stated in the code: OpenFIGI may return
corrected values for those types. `tryOpenFIGIFromHints` already reports which
hint produced the results, so the signal needs no new plumbing.

What that signal does not carry is correctness. A non-empty response proves the
code exists and maps to a security, not that it maps to this one, because an
invented ISIN that looks plausible is usually a real ISIN belonging to something
else. 0132 is the check that closes the gap.
