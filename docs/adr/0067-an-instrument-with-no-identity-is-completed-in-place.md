# An instrument with no identity is completed in place

A broker-description-only instrument is one row and one non-canonical
`BROKER_DESCRIPTION`: it holds no canonical identifier and every column is null.
It exists because an upload had to attach a transaction to something and nothing
identified the security. When a later resolution does identify it,
`EnsureInstrument` writes what it found on to that instrument, filling the null
columns and inserting the identifiers, rather than matching it and discarding
everything but the match.

That is a write on to an existing instrument, which is otherwise refused. The
refusal has two reasons and neither reaches this case.

The first is that a set of identifiers the resolver assembled from several
results is not an association anybody stated, so acting on it merges securities
nobody said were one
([0060](0060-an-identity-claim-is-admitted-by-the-authority-for-its-scope.md),
issue [0140](../issues/0140-a-merge-requires-a-corroborated-association.md)).
Here there is nothing to merge with. The instrument names no security, so the
identifiers arriving are not being associated with an identity -- they are
becoming the first one it has. The same write on to an instrument that already
holds a canonical identifier stays refused, and what may be added to one is
issue [0136](../issues/0136-an-instrument-accumulates-what-resolution-learns.md),
asked under the corroboration rule this does not need.

The second is that the identifier is the source of truth for an existing
instrument, so a stored value is never replaced
([0004](0004-instrument-resolution-and-merge.md)). Nothing is replaced: every
column filled was null and every identifier inserted was absent. A name already
held keeps the `valid_from` it was written with, because when a name became
correct is a market fact and re-seeing it is not evidence of a new one; an
inserted name carries the resolution's own vintage
([0055](0055-identifier-validity-is-an-interval.md)).

## Consequences

It is what lets a hinted upload bind to the instrument its description already
names. That upload states identifiers the database does not recognise, so the
identifier lookup passes over an instrument findable by nothing but its
description, and a second one used to be minted beside the first -- which kept
the transactions already attached to it (issue
[0135](../issues/0135-a-hinted-re-upload-forks-a-new-instrument.md)). Binding
alone would stop the fork and lose the identification with it: the instrument
would gain no identifier and every later upload would pay the identifier plugins
over again.

The binding does not chain an association through the description. A broker
description is not injective and may mediate nothing
([0061](0061-transitivity-needs-a-non-reassigned-identifier.md)), and it
mediates nothing here: only an instrument with no identity is bound to, so there
is no second identity for the description to reach. A description naming an
instrument that has since been identified is left alone, and the upload resolves
by its identifiers as it would have.

Storing nothing on the hinted path still stands: the `BROKER_DESCRIPTION` the
binding names is one already in the database, so it matches rather than inserts,
and no description-derived mapping is minted to pollute later lookups. Whether
one should be is issue
[0106](../issues/0106-share-a-broker-description-map.md), and answering it amends
[0004](0004-instrument-resolution-and-merge.md).
