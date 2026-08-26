# An instrument carries the authority of the channel that named it

[0060](0060-an-identity-claim-is-admitted-by-the-authority-for-its-scope.md),
[0061](0061-transitivity-needs-a-non-reassigned-identifier.md) and
[0063](0063-identity-claims-are-owned-until-users-corroborate-them.md) each
govern a claim at the moment it arrives. None of them says what is true of an
instrument afterwards, so the property they exist to preserve is spelled out
nowhere and is held instead by the mechanisms that happen to preserve it. A
broker-description-only instrument is a *shape* -- one row, one non-canonical
identifier, columns left null -- rather than a class with a rule attached, and
each write path maintains that shape independently. Where one of them does not,
nothing notices: ingestion fills the name with the broker's own text, the
completion never takes it back, and a security ends up wearing a statement line
as its name (issue
[0169](../issues/0169-unconfirmed-metadata-is-discarded.md)).

Stated as an invariant instead:

> Every identifier and every piece of metadata on an instrument has been
> confirmed by a source authoritative for that instrument's class. A
> **system-authoritative** instrument holds at least one identifier that arrived
> through a channel carrying system authority -- an identifier plugin, an admin
> upload, reference data. A **user-authoritative** instrument holds only
> identifiers and metadata that arrived through a user-mediated channel.

Two classes, and each is closed under its own authority: a user-mediated source
may write freely to a user-authoritative instrument and may not write to a
system-authoritative one on its own account.

## The axis is the channel, not the scope of the identifier

Every user-authoritative identifier today is broker-scoped -- a
`BROKER_DESCRIPTION`, and a contract identifier once
[0123](../issues/0123-carry-broker-contract-identifiers.md) adds one -- which
makes scope look like the discriminator. It is a coincidence of the channels we
have. A broker is the only authority for its own contract numbers
([0060](0060-an-identity-claim-is-admitted-by-the-authority-for-its-scope.md)),
and what costs those values their standing is that the only route they take into
this system is a file a user handed us, unauthenticated and impossible to
re-interrogate
([0062](0062-a-user-mediated-claim-is-a-lead-not-a-write.md)). Given a direct
feed from the broker, a contract identifier would carry system authority exactly
as an identifier plugin's answer does, and nothing about the identifier itself
would have changed.

So scope is the current **proxy** for the channel and not the thing itself.
[0063](0063-identity-claims-are-owned-until-users-corroborate-them.md)'s owner
column is what replaces it: null means the row was written by a system authority,
and non-null names the user whose file carried it. An implementation that reads
the class off the identifier type will keep working right up to the first
promotion, and will then call a promoted row user-authoritative for good.

This does not reopen
[0061](0061-transitivity-needs-a-non-reassigned-identifier.md)'s "scope is the
wrong axis". That answers *what may mediate a chain*, where the counterexample is
`MIC_TICKER` -- global by namespace and reassigned constantly. This answers *who
is the authority*, which is
[0060](0060-an-identity-claim-is-admitted-by-the-authority-for-its-scope.md)'s
own question. The two axes are orthogonal and both are needed: authority says
whether a claim may be acted on at all, reassignment says whether a value may
carry one through.

## Metadata is governed by the same rule, and needs no column to say so

A name, an asset class, a CIK: none of them is an identifier, and each is
routinely supplied by whatever wrote the row. On a user-authoritative instrument
every one of them arrived through a user-mediated channel, because by definition
nothing else has written to it. So the class of the instrument *is* the
provenance of its metadata, and no per-column provenance has to be recorded to
know it.

What follows is the operative rule:

> When a user-authoritative instrument becomes system-authoritative, its own
> metadata is discarded and replaced by what the system authority supplied. It is
> not merged with it, and it does not win on the strength of having been stored
> first.

The alternative is a provenance column per metadata field, which buys one thing
this does not: a system-authoritative instrument holding a user-supplied value
for a field no authority has answered. We decline it. In practice a system
authority that identified the security has already supplied the fields it knows,
so the user-authoritative row has little to contribute and what it does
contribute is unverified; carrying provenance for every column to preserve it
would cost more than the value is worth.

## What this settles about a merge

A merge is admitted by the authority of the claim, never by an exemption for the
kind of instrument at the end of it. A user-mediated claim tying a broker name to
a system-authoritative instrument therefore does not merge the two: it writes an
**owned identifier row** on the system-authoritative instrument
([0063](0063-identity-claims-are-owned-until-users-corroborate-them.md)), and
the user-authoritative instrument stays where it is for everyone still resolving
through it.

That is also why user-authoritative instruments can stay instance-global and
system-owned while ownership lives on identifier rows. Sharing one is safe
because its metadata is provisional -- the rule above discards it the moment an
authority arrives -- and because a user-owned association mediates nothing
([0061](0061-transitivity-needs-a-non-reassigned-identifier.md)), so nothing
merges such an instrument away, and nothing moves one user's postings on another
user's word, until the promotion sweep has made the mapping system-owned. At that
point moving every user's postings is precisely what the corroboration was for.

## Consequences

- [0067](0067-an-instrument-with-no-identity-is-completed-in-place.md) is the
  special case of this rule where the instrument has no metadata worth
  discarding, rather than an exception to
  [0004](0004-instrument-resolution-and-merge.md).
- [0004](0004-instrument-resolution-and-merge.md)'s "the identifier is the source
  of truth for an existing instrument, so a stored value is not overwritten"
  protects a value a system authority wrote. A value on a user-authoritative
  instrument is a claim awaiting confirmation, and being stored first earns it
  nothing.
- `instrument_identifiers.canonical` is a poor proxy for the class. It is false
  for `BROKER_DESCRIPTION` and true for everything else, so a contract identifier
  arriving with 0123 reads as system-authoritative on a row nobody vetted. The
  question its readers want is the owner column.
- The two classes have to be nameable in SQL as well as in Go, since
  `holdsNoCanonicalIdentifier` and `db.Identified` ask this question at a point
  where no row has been loaded.
