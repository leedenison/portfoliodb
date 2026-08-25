---
status: closed
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

## Outcome

Each question is settled once, and the asset class one turned out to be about
the vocabulary rather than about the predicates.

**Asset class had no way to say "a share or a fund"**, so every site that
compared a stated class to a resolved one invented its own tolerance, and the
non-transitive pair table was the largest of them: a workaround for the missing
value rather than a rule anyone wanted. The vocabulary is now a tree, as the
transaction types have been since adr/0044, with `EQUITY` over STOCK, ETF and
MUTUAL_FUND, `DERIVATIVE` over OPTION and FUTURE, and `SECURITY` where UNKNOWN
used to stand -- which freed UNKNOWN to be the root and folded `InstrumentKind`,
a second vocabulary with a second gate, into the one tree. `db.IsDerivative`
went the same way. A source now says what it can defend: an OFX `BUYSTOCK` is
`EQUITY`, because OFX has no ETF tag.

**Seven sites, two questions.** `assetclass.Contradicts` is permissive and
symmetric -- two claims disagree when no reading admits them both -- and gates
ingest validation and every reported hint difference. `assetclass.Corroborates`
is strict and asymmetric: the claim must have ruled something out and the answer
must fall inside it, so EQUITY is corroborated by ETF and STOCK is not
corroborated by EQUITY. Routing asks `MayBe` over what a plugin declares, so a
plugin declaring STOCK is reached by the EQUITY a statement line says. Nothing
compares two classes for equality, and every call site says which question it is
asking.

Two bugs fell out, both of a plugin answering more specifically than its
provider had. The Massive identifier plugin read `market == "stocks"` and
answered STOCK, throwing the ticker type beside it away, so every ETF resolved
through it was stored as a share in a company. And OpenFIGI's fallback on
`marketSector == "Equity"` answered STOCK, where that sector holds equity
options, single stock futures, warrants and rights beside shares and funds --
the recorded responses show an equity option carrying it. Both answer SECURITY
where the provider left it open, the vocabulary having no node for "a security
in the equity market sector" and SECURITY being the nearest one that is true.

**The currency family holds on every path.** `pluginutil.PluginAccepts`,
`PluginAcceptsListing`, `inflationfetcher.pluginAcceptsCurrency` and the ONS
plugin's own guard below it go through `currency.Same`, so a plugin declaring
GBP is offered a line stored as GBX. The spec sentence that claimed this was
true is now true.

`currency.Same` had claimed to be the only currency comparison in the Go code,
which was the wrong claim to make rather than a false one: the places asking
whether two codes are the same *unit* -- a posting quoted in pence and settled
in pounds names one line and still needs its decimal point moved -- are asking a
different question and compare codes directly. It now says which question it
answers.

**The venue predicates were a naming problem, and stay three.** The three
representations are real and `venueStated`'s exclusion of a composite exchange
code was right; what was wrong was its name and its comment, which claimed a
question it does not ask. It is `micStated`, it says it asks whether a source
named something comparable against a MIC, and it names the three predicates it
is not -- `ingestion.identityComplete`, which counts a composite because it asks
whether the identity picks out a listing; `pluginutil.anyLineHasVenue`, on a
stored row; and `identifier.Venue.Known`, on a provider's answer. Folding
`identityComplete` into the properties table is 0165, and naming `Venue.Known`
is 0164.
