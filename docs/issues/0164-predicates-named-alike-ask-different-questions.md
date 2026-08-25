---
status: closed
title: Predicates named alike ask different questions
---

The identity predicates have grown to around thirty-four, and several pairs are
told apart only by reading their bodies.

**`Known` means three things, two of them in one package.**
`identifier.Known(t)` is membership of the controlled vocabulary.
`identifier.Venue.Known()` is whether any venue information is present. The
`Known` struct in `plugins/openai/candidate` is what the source stated. The first
two are the pair that matters: same package, same spelling, unrelated questions.

**`Venue.Agrees` against `venueAgrees`.** The method compares two `Venue` values
symmetrically at whichever precision they share. The function in
`identification` checks one MIC against the domains of stated `MIC_TICKER`
identifiers. One caller each, and nothing but the receiver to tell them apart at
a call site.

**The agreement family runs to seven.** `consistentWith`, `corroborated`,
`CorroboratesSecurity`, `sameSubject`, `sameCurrency`, `confirmedFields` and
`ConfirmedDBFields`, with `lineMismatch` and `idMismatch` stating the negative.
Each is documented well on its own. What is missing is a statement of the shape
of the family -- line admission, then security corroboration, then field
confirmation -- so a reader has to reconstruct it from seven doc comments and
work out which two of the seven are the same question at two grains.

**Plugin admission has four predicates in three packages under four naming
conventions**: `PluginAccepts` at security grain, `PluginAcceptsListing` at line
grain, `ShouldAttemptPlugin` on hints, and `pluginAcceptsCurrency` in the
inflation fetcher.

**`identityComplete` and `completesPartialIdentity`** read as the same question.
The first asks whether the stated identifiers already pick out a listing; the
second asks whether the run is a broker upload.

Rename where the name is the problem, and where several predicates are wanted
give the family a single place that says what each one is for.

## Outcome

Every collision was a naming problem, and one of the families turned out to want
a home rather than a convention.

**`Known` is one question again.** `identifier.Venue.Known` is `Venue.Named`,
which is the vocabulary the type's own doc comment already used: a provider
names either a venue or a market, and this asks whether it named one of them.
Not `Stated` -- this repository reserves that for what a source said, which is
what `Identity.Stated` and `micStated` are. `identifier.Known` keeps the name,
being the one that really is about a name being known to the system, and now
says which nearby question it is not. `candidate.Known` is left alone: it is a
struct in another package holding what the source stated, and nothing reads it
as a predicate.

**`venueAgrees` is `micAmongStated`,** and is documented as `micStated`'s second
half -- that one asks whether a source named a venue at all, this one whether it
named this venue. `Venue.Agrees` keeps its name, being the one that really does
compare two `Venue` values, and says so: two provider answers, symmetrically, at
whichever precision they share.

**The agreement family has a package doc comment** in
`server/service/identification/doc.go` stating the shape the seven doc comments
left a reader to reconstruct: line admission, then security corroboration, then
field confirmation, with `sameSubject` underneath all of them. It records that
stages one and two are asked of one plugin result against another while stage
three is asked of the chosen answer against what the caller knew -- which is why
stage three exists twice, once per grain of answer, and why the database pair
tests a currency by membership where the plugin pair tests it by equality.

The roll call in this issue had gone stale before it was read: `sameCurrency`
left with 0162, replaced by `currency.Same`. `venueAgrees` had two callers
rather than the one claimed.

**Plugin admission is four predicates in one package under one convention.**
`pluginutil.Accepts` (a stored security), `AcceptsListing` (a stored line),
`AcceptsSecurityType` (a security type a source stated) and `AcceptsCurrency`
(a currency code), each named for its subject, under one doc comment saying
which grain each asks at. `identifier.ShouldAttemptPlugin` and
`inflationfetcher.pluginAcceptsCurrency` moved; the two `PluginAccepts*` names
dropped a prefix that restated their package.

They were spread over three packages under four spellings, which is how the
currency family came to hold on three of the paths and not the fourth until
0163 went looking. Together, the one thing that is genuinely not uniform is
visible: the first two test set membership of a class the system stored, while
the third asks `assetclass.MayBe` of a class a source stated, which may be a
node above anything a plugin declares.

**`completesPartialIdentity` is `mayPayForCompletion`,** taking its name from
what its own first sentence already said. It reads nothing but `RunKind`, and
now says that is what separates it from `identityComplete` on the line below it
at the call site. `identityComplete` keeps its name; its body is 0165.
