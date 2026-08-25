---
status: open
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
