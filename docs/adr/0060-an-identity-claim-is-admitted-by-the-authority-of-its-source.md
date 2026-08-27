# An identity claim is admitted by the authority of its source

Resolution grades identifiers by provenance: stated by a source, proposed by a
candidate plugin, returned by an identifier plugin. Merging does not act on
identifiers. It acts on the claim that two of them denote one instrument, and nothing
grades that. `EnsureInstrument` merges whenever the identifier set it is handed lands
on two rows -- a set the resolver assembled from several plugin results, so the claim
it merges on is one nobody made.

The grading is at the wrong grain. A value is a node; a merge is an edge. So claims are
classified by what they attach, and each kind is admitted only by a source with the
authority to make it:

> An identifier does not enter the database unless a source authoritative for it
> validated it. An association does not enter unless one corroborated it.

## Authority is a level a source carries

**System authority** is what an identifier plugin, reference data and an admin's
archive carry: an authenticated channel, repeatable, and one that can be asked again
when an answer starts to look wrong. **User authority** is what a transaction upload
carries -- a broker file, or the postings of a user archive -- which is
unauthenticated, single-shot and impossible to re-interrogate
([0062](0062-a-user-mediated-claim-is-not-a-write-to-shared-data.md)).

Which source within a level does not matter. Every identifier plugin is equally
authoritative for a global identifier, so attribution decides nothing; the level is the
whole of the question.

What the two levels produce is worth naming, because every other identity rule reads in
these terms:

> A **fact** is an association or a piece of metadata the system treats as settled: it
> arrived from a system-authoritative source, or a system-authoritative source
> corroborated it. A **claim** is one the system treats as possibly true and not yet
> settled: it arrived from a user-authoritative source and nothing has corroborated it.

Facts are stored system-owned and claims are stored owned by the user whose file
carried them, so the owner column is where the distinction lives
([0063](0063-identity-claims-are-owned-until-users-corroborate-them.md)).

**Authority is not read off the identifier.** Any type can arrive at either level: an
ISIN stated in a broker file is a claim, an ISIN an identifier plugin returned is a
fact, and they are the same value. The scope of an identifier -- a registry's
namespace, one broker's numbering, one source's own text -- says what the value is good
for and nothing about who vouched for it. Scope looks like the discriminator only
because the values a broker alone can supply are also the ones that reach us only
through a user
([0079](0079-an-instrument-carries-the-authority-of-the-source-that-named-it.md)).

## The kinds of claim

**An identifier to an identifier.** The only kind that may merge, and it has three
sub-kinds because the channels able to make them differ:

- *Two global identifiers* (ISIN to CUSIP). An identifier plugin can make it -- by
  returning both, or by returning one while **strictly filtering** on the other -- so
  it arrives with system authority and is admitted as a fact. A candidate plugin may
  propose the pair; a proposal is not a claim and must be confirmed before anything
  acts on it.
- *A broker-scoped identifier to a global one* (CONID to CUSIP). Only the broker knows
  what its own contract numbers mean, so no identifier plugin can confirm or refute it
  and a candidate plugin cannot make it at all. **But the broker is not who we heard it
  from**: the only route such a claim takes into this system is a file a user uploaded,
  so it arrives with user authority and is admitted as a claim -- owned by that user,
  mediating nothing -- until enough other users' own channels agree and an admin's
  sweep promotes it. Authoritative in content and untrusted in delivery are not in
  tension: the first is why the value is worth keeping, and the second is what the
  ownership column holds.
- *A broker description to a global identifier*. The same channel and the same level. A
  candidate plugin cannot make this claim either: what it supplies is a key to query,
  and what is stored is the identifier plugin's result bound to the description by the
  resolver -- so the association carries the authority of the resolution, not of the
  guess that seeded it, and where the key was invented outright
  [0059](0059-an-invented-identifier-round-trips.md) is what makes the result earn its
  place.

**Metadata to an identifier** (a currency to an ISIN). Only a system-authoritative
source, and never a merge: two results agreeing about a currency and a venue have not
said they are the same security, and treating that as identity is how two share classes
on one venue become one instrument.

**A transaction to an identifier.** A broker states it, so it arrives with user
authority, and it never triggers a merge on its own.

## A strict filter states a claim as loudly as a payload does

A provider asked to map an ISIN, which answers "no identifier found" when the value
matches nothing, has asserted that the security it describes is the one that ISIN
names -- whether or not the ISIN appears in what it sent back. The confirmation is the
response, not the value, which
[0057](0057-a-proposed-identifier-is-not-evidence.md) records at the grain of a single
identifier; the same is true of the association.

This is not a future concern. The OpenFIGI plugin deliberately does not append a
matched ISIN or CUSIP, because the provider may return a corrected value for those
types. Grading only what is returned makes that proof invisible in the
highest-precedence plugin we have.

**Strictly** is doing the work. A filter the provider silently relaxes when it matches
nothing is a hint, and a response to one confirms nothing -- it is the echo that a real
filter merely resembles. So which filters are strict is something an identifier plugin
declares and a call records, alongside what it returned
([0065](0065-a-plugin-declares-what-it-claims-a-call-records-what-it-claimed.md)).

## The two failure modes to close

The first half of the rule already held: identifiers written come solely from what
identifier plugins returned, and a stated hint is used for ranking and comparison but
never written. The archive import path does write what the file states, and only a
system archive carries instruments and importing one is admin-only -- which is the
system authority an admin archive is meant to have
([0063](0063-identity-claims-are-owned-until-users-corroborate-them.md)).

The second half held nowhere, and in both directions at once. Flattening every
identifier plugin result into one list before the merge site sees it **invents** claims:
two plugins returning disjoint types have nothing to disagree about, so they are
consistent by default and their identifiers are unioned on the strength of agreeing
about currency and venue. Not carrying a claim made by filtering **discards** real ones:
OpenFIGI mapping a stated ISIN and answering with a FIGI reaches the merge site as a
FIGI with no ISIN beside it, so the association the provider proved is not there to act
on.

## Admission is re-evaluated, not decided once

A transaction's identifiers cannot merge on their own authority, but an identifier
plugin may later corroborate the association between them -- when a provider starts
returning a field it did not, or when an admin enables a plugin or pays for a richer
tier. That corroboration is what turns a claim into a fact, and the promotion sweep of
[0063](0063-identity-claims-are-owned-until-users-corroborate-them.md) is the other
route to the same place. Usually there is nothing left to learn, because a plugin will
already have corroborated every link it could. The point is that the answer can change
without the stored claims changing.

So corroboration is a question asked of stored claims rather than a gate applied at
write time, and the periodic re-identification the spec already calls for is where it
is asked again.

## Consequences

- [0057](0057-a-proposed-identifier-is-not-evidence.md) closed one route into the merge
  set by barring a proposal from being returned. This closes the other: the set is no
  longer a set. What reaches the merge site is the results, kept apart.
- The eager merge in [0004](0004-instrument-resolution-and-merge.md) survives for the
  case it was written for -- one plugin returning an ISIN and a CUSIP for one security
  is exactly a corroborated edge. What stops is merging on a union.
- Every claim has to carry the level of authority it arrived with, because the merge
  site cannot recover it afterwards. A stored row records it as its owner; a claim in
  flight has to say so.
- Nothing here says which claims may be chained. That is
  [0061](0061-transitivity-needs-a-non-reassigned-identifier.md), and it asks about the
  identifier rather than about the source: whether the mediating value could have
  denoted something else at the time. The two questions are orthogonal and both are
  asked.
