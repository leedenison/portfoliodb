---
status: open
title: Overlapping predicates answer one question with different tolerances
milestone: M25
---

Three questions about instrument data each have several predicates answering
them, and the answers differ. This is worse than duplication: the copies are not
copies, so nothing looks wrong at any one site.

**Asset class agreement, three tolerances.** `db.IsAssetClassCompatible` treats
STOCK, ETF and MUTUAL_FUND as pairwise-but-not-transitively equivalent and lets
UNKNOWN accept anything but CASH; it gates ingest validation. The SecurityType
test inside `identification.CompareHints` is a strict `EqualFold` with UNKNOWN
skipped; it decides whether a hint difference is reported.
`identifier.ShouldAttemptPlugin` is exact set membership with UNKNOWN skipped; it
decides which plugins are called. A broker calling an ETF a STOCK therefore
passes ingest, is reported as a difference by identification, and may be routed
to a different plugin set. None of the three references the others, and nothing
says which tolerance is the intended one for which stage.

**The currency family rule has a path that does not hold it.**
`pluginutil.PluginAccepts` and `PluginAcceptsListing` compare
`strings.ToUpper(currency)` against the plugin's declared set, so a price plugin
declaring GBP is not offered a line stored as GBX. docs/spec/identifiers.md says
every currency comparison uses the family, because a rule about what makes two
lines cannot hold on one path and not another. Either this path adopts the family
or the spec sentence stops claiming it does.

**"Do we know where it trades" has three predicates over three representations**:
`identifier.Venue.Known` on a plugin's answer, `identification.venueStated` on
stated identifiers, `pluginutil.anyLineHasVenue` on a stored row. The three
representations are real and probably need three functions, but the predicates
also disagree about what counts: `venueStated` reads only `MIC_TICKER` domains,
while `ingestion.identityComplete` treats an `OPENFIGI_TICKER` domain as naming a
listing too. Excluding a composite exchange code from a test that compares
against a MIC is defensible; the two predicates answering "did a source say
where" differently is not, unless it is said somewhere that they do.

Settle each question once, and where several predicates are genuinely wanted have
each say which of the others it is not.
