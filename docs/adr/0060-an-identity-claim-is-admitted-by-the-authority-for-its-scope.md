# An identity claim is admitted by the authority for its scope

Resolution grades identifiers by provenance: stated by a source, proposed by a
candidate plugin, returned by an identifier plugin. Merging does not act on
identifiers. It acts on the claim that two of them denote one instrument, and
nothing grades that. `EnsureInstrument` merges whenever the identifier set it is
handed lands on two rows -- a set the resolver assembled from several plugin
results, so the claim it merges on is one nobody made.

The grading is at the wrong grain. A value is a node; a merge is an edge. So
claims are classified by what they attach, and each kind is admitted only by
whoever is the authority for that kind.

## The kinds of claim

**An identifier to an identifier.** The only kind that may merge, and it has
three sub-kinds because the authority differs:

- *Two global identifiers* (ISIN to CUSIP). Authoritative from one identifier
  plugin result naming both -- by returning both, or by returning one while
  **strictly filtering** on the other (see below). A candidate plugin may propose
  the pair; that is not authoritative and must be confirmed before anything acts
  on it.
- *A broker-scoped identifier to a global one* (CONID to CUSIP). Only the broker
  can make this claim, because only the broker knows what its own contract
  numbers mean, and no identifier plugin can confirm or refute it. A candidate
  plugin cannot make it at all. **But the broker is not who we heard it from**:
  the only route such a claim takes into this system is a file a user uploaded,
  so it is admitted owned by that user and mediates nothing until enough other
  users' own channels agree and an admin's sweep promotes it
  ([0062](0062-a-user-mediated-claim-is-a-lead-not-a-write.md),
  [0063](0063-identity-claims-are-owned-until-users-corroborate-them.md)).
  Authoritative in content and untrusted in delivery are not in tension; they are
  what the ownership column exists to hold apart.
- *A broker description to a global identifier*. The broker may make it, arriving
  by the same channel and under the same ownership: a file supplying an ISIN
  alongside its own description has no reason to be doubted, and no reason to be
  trusted further than the user who sent it. A candidate plugin cannot make this
  claim either. What it supplies is a key to query, and what is stored is the
  identifier plugin's result bound to the description by the resolver -- so the
  association carries the authority of the resolution, not of the guess that
  seeded it, and where the key was invented outright
  [0059](0059-an-invented-identifier-round-trips.md) is what makes the result
  earn its place.

**Metadata to an identifier** (a currency to an ISIN). Only an identifier plugin.
Never merges: two results agreeing about a currency and a venue have not said
they are the same security, and treating that as identity is how two share
classes on one venue become one instrument.

**A transaction to an identifier.** Only a broker. It never triggers a merge on
its own -- but see below, because it does not follow that the identifiers it
carries can never take part in one.

## A strict filter states a claim as loudly as a payload does

A provider asked to map an ISIN, which answers "no identifier found" when the
value matches nothing, has asserted that the security it describes is the one
that ISIN names -- whether or not the ISIN appears in what it sent back. The
confirmation is the response, not the value, which
[0057](0057-a-proposed-identifier-is-not-evidence.md) already records at the
grain of a single identifier. The same is true of the association: a result
returning a FIGI while strictly filtered on an ISIN corroborates that pair
exactly as firmly as returning both would.

This is not a future concern. The OpenFIGI plugin deliberately does not append a
matched ISIN or CUSIP, because the provider may return a corrected value for
those types, and its own comment records that a successful mapping response
proves the association. Grading only what is returned makes that proof invisible
in the highest-precedence plugin we have.

**Strictly** is doing the work. A filter the provider silently relaxes when it
matches nothing is a hint, and a response to one confirms nothing -- it is the
echo that a real filter merely resembles. So which filters are strict is
something an identifier plugin declares and a call records, alongside what it
returned
([0065](0065-a-plugin-declares-what-it-claims-a-call-records-what-it-claimed.md)).

## Admission

The rule, normatively:

> An identifier does not enter the database unless validated by the authority for
> its scope. An association does not enter unless corroborated by one.

### Where the code stands against it

Descriptive, and the reason this ADR is being written rather than merely
recorded.

**The first half already holds, throughout.** `mergedIds` is built solely from
what identifier plugins returned, `ident.Stated` is passed to them and used for
ranking and comparison but never written, and `InsertInstrumentIdentifier` has no
caller. The archive import path does write what the file states, but only a
system archive carries instruments and importing one is admin-only, which is the
authority level an admin archive is meant to have
([0063](0063-identity-claims-are-owned-until-users-corroborate-them.md)). A user
archive has no instrument part to state.

**The second half holds nowhere**, because the resolver flattens every
identifier plugin result into one list before the merge site sees it, and because a claim made by
filtering is not carried at all. Two identifier plugins returning disjoint
types have nothing to disagree about, so they are consistent by default and their
identifiers are unioned on the strength of agreeing about currency and venue.
That union is the manufactured claim, and it merges. Meanwhile a real claim --
OpenFIGI mapping a stated ISIN and answering with a FIGI -- reaches the merge
site as a FIGI with no ISIN beside it, so the association the provider proved is
not there to act on. The code both invents claims nobody made and discards ones
that were made.

## Admission is re-evaluated, not decided once

A transaction's identifiers cannot merge on their own authority, but an
identifier plugin may later corroborate the association between them -- when a
provider starts returning a field it did not, or when an admin enables a plugin
or pays for a richer tier. Usually there is nothing left to learn, because a
plugin will already have corroborated every link it could. The point is that the
answer can change without the stored claims changing.

So corroboration is a question asked of stored claims rather than a gate applied
at write time, and the periodic re-identification the spec already calls for is
where it is asked again.

## Consequences

- [0057](0057-a-proposed-identifier-is-not-evidence.md) closed one route into the
  merge set by barring a proposal from being returned. This closes the other: the
  set is no longer a set. What reaches the merge site is the results, kept apart.
- The eager merge in [0004](0004-instrument-resolution-and-merge.md) survives for
  the case it was written for -- one plugin returning an ISIN and a CUSIP for one
  security is exactly a corroborated edge. What stops is merging on a union.
- Nothing here says which claims may be chained. Corroboration through a third
  identifier is [0061](0061-transitivity-needs-a-non-reassigned-identifier.md).
