---
status: open
title: A claim is owned only where nothing can adjudicate it
milestone: M24
dependencies: [0171, 0177]
---

0142 decides what to own by scope: a broker-scoped identifier or a broker-description
association is written owned by the user who supplied it, and everything else is a fact.
adr/0079 says that is a coincidence of the channels available rather than a rule -- the
values a broker alone can supply are also the ones that reach us only through a user.
The rule it is a coincidence of is this: a claim is owned where nothing could have
settled it.

## Four fates, decided by the enabled plugin set

- **Corroborated.** A plugin declares it can adjudicate the claim, was asked, and made
  it: by returning both values, or by returning one while strictly filtering on the
  other. The claim is a fact and is stored system-owned. This already happens, and what
  is written is the plugin's answer rather than the user's statement (adr/0060).
- **Declined.** A plugin declares it can adjudicate the claim and did not make it.
  Nothing is stored. Owning it would leave 0142's sweep able to promote, by counting
  users, precisely what an authority declined to confirm -- and every user reads the same
  mapping out of the same security master, so the count rises without a second opinion
  ever arriving.
- **Unadjudicable.** No enabled plugin declares knowledge of the claim. Nothing could
  settle it, so it is stored owned by the user who supplied it, for a plugin an admin
  enables later, for an admin's own hand (0168), or for the sweep.
- **Refused.** Acting on it would have to change a stored fact. Nothing is stored and the
  transaction resolves to one winner, which is 0143.

Broker-scoped identifiers land in the third and stop being special. So do global
identifiers on an instance with no identifier plugin enabled, which is the case that
shows the discriminator is the plugin set and not the vocabulary.

0142 is closed and its scope wording is in shipped code and in the spec, so this restates
that rule rather than extending it.

## Gating on a declaration

adr/0065 holds two surfaces apart -- what a plugin declares it claims, and what a call
recorded claiming -- and rules that only the record may gate, a declaration being an
unenforced promise. The third case reads a declaration and is a gate, so 0065 is amended
rather than worked around. The bar is defensible here: the plugin set is ours, its
declarations are reviewed with its code, and both ways of being wrong are recoverable. A
plugin that declares more than it can do withholds a claim re-identification would have
settled anyway; one that declares less stores a claim re-identification then settles.
Neither writes a fact on the strength of a comment, which is what 0065 was defending
against.

Both surfaces are still needed, and only one of them is the declaration. Whether anything
*could* have adjudicated the claim is the declaration, which no plugin makes today and
0177 builds; whether anything *did* is the record, which `Result.Identifiers` and
`Result.Filtered` already carry. The record read here is the resolution's own results, in flight, so nothing is
read back out of a table -- which is what keeps this clear of adr/0080's rule that no
functional path reads telemetry.

## The listing grain gains an owner

`instrument_listing_identifiers` carries no owner, and `findListingHolder` documents that
as sound because nothing user-mediated is stored at that grain. `SEDOL`,
`OPENFIGI_COMPOSITE`, `MIC_TICKER` and `OPENFIGI_TICKER` are listing-grain and a broker
file states tickers, so the broadening breaks it: where the enabled plugins declare
nothing about a ticker, that ticker is the third case and wants a user-owned row at
listing grain. That is the column, an owner-scoped lookup on a second hot path, and the
exclusion constraint change 0142 made at security grain, again. Its own increment rather
than a line in this one.

## The answer changes when the plugin set does

A claim unadjudicable today becomes adjudicable the moment an admin enables a plugin, so
the classification is a property of the enabled set rather than of the row. adr/0060
already has admission re-evaluated rather than decided once, and names one direction: a
claim becomes a fact. This adds the other. Re-identification asks the newly able plugin,
and a claim it declines is deleted rather than left owned -- the second case's argument
is defeated outright if arriving before the plugin did is enough to keep a claim alive.
Deleting one moves the postings that resolved through it, which is the move 0172 already
makes.

See adr/0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md,
adr/0063-identity-claims-are-owned-until-users-corroborate-them.md and
adr/0079-an-instrument-carries-the-authority-of-the-source-that-named-it.md.
