# An instrument carries the authority of the source that named it

[0060](0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md),
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
> from a source carrying system authority -- an identifier plugin, an admin
> upload, reference data. A **user-authoritative** instrument holds only
> identifiers and metadata that arrived from user-authoritative sources.

Two classes, and each is closed under its own authority: a user-mediated source
may write freely to a user-authoritative instrument and may not write to a
system-authoritative one on its own account.

## The class is read from the owner, never from the identifier

Every user-authoritative identifier today is broker-scoped -- a
`BROKER_DESCRIPTION`, and a contract identifier once
[0123](../issues/0123-carry-broker-contract-identifiers.md) adds one -- which
makes scope look like the discriminator. It is a coincidence of the channels we
have, and
[0060](0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md)
gives the argument: authority is the level a source carries, and any identifier
type can arrive at either level.

The instrument's class follows from that, so it is read from
[0063](0063-identity-claims-are-owned-until-users-corroborate-them.md)'s owner
column: an instrument is system-authoritative when it holds at least one
system-owned row. An implementation that reads the class off the identifier type
instead will keep working right up to the first promotion, and will then call a
promoted row user-authoritative for good.

This does not reopen
[0061](0061-transitivity-needs-a-non-reassigned-identifier.md)'s "scope is the
wrong axis". That answers *what may mediate a chain*, where the counterexample is
`MIC_TICKER` -- global by namespace and reassigned constantly. This answers *who
is the authority*, which is
[0060](0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md)'s
own question. The two axes are orthogonal and both are needed: authority says
whether a claim may be acted on at all, reassignment says whether a value may
carry one through.

## Metadata is governed by the same rule, and needs no column to say so

A name, an asset class, a CIK: none of them is an identifier, and each is
routinely supplied by whatever wrote the row. On a user-authoritative instrument
every one of them arrived from a user-authoritative source, because by definition
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

A merge is admitted by the authority of the claim asking for it, never by an
exemption for the kind of instrument at either end. Two questions decide it: what
level of authority the claim arrived with, and what acting on it would change.

- **A claim carrying system authority settles the link.** The instruments are one
  and merge outright. Every association either of them held was already a fact and
  stays one.
- **A claim carrying user authority may move only what is itself a claim.** The
  user's own identifier rows are repointed onto the other instrument, along with
  the postings that resolved through them. Nothing on the target is rewritten: its
  facts are untouched and no row changes owner. The instrument the rows left
  stands for its other owners, whose own rows still name it -- more than one
  user's claim can point at one user-authoritative instrument, so what looks like
  a merge from one user's side is a repointing from the instance's.
- **A claim carrying user authority that would have to move a fact is refused.**
  Acting on it would settle an association on the strength of one unauthenticated
  file, which is the promotion the sweep of
  [0063](0063-identity-claims-are-owned-until-users-corroborate-them.md) exists to
  do properly.

A refusal is not permanent. A later plugin result and the sweep are two routes by
which the claim becomes a fact, and admission is re-evaluated rather than decided
once
([0060](0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md)).

The metadata rule above does not travel with a repointing. What moves is the
identifier row and the postings that hang off it; the user-authoritative
instrument's metadata is unconfirmed and is dropped rather than carried onto the
instrument receiving them. Preserving it would be the per-column provenance
declined above.

This is also why user-authoritative instruments can stay instance-global while
ownership lives on identifier rows. Sharing one is safe because its metadata is
provisional -- the rule above discards it the moment an authority arrives -- and
because a claim mediates nothing
([0061](0061-transitivity-needs-a-non-reassigned-identifier.md)), so nothing
merges such an instrument away and nothing moves one user's postings on another
user's word until the sweep has made the mapping system-owned. At that point
moving every user's postings is precisely what the corroboration was for.

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
- The survivor of a merge is always system-authoritative, and that follows from
  the admission rule rather than from a tie-break: merging outright needs a claim
  carrying system authority over associations that are already facts, and a
  user-authoritative instrument holds none. So there is one place an instrument
  stops being user-authoritative under its own id -- the completion of
  [0067](0067-an-instrument-with-no-identity-is-completed-in-place.md) -- and the
  metadata rule has one write site rather than two (issue
  [0169](../issues/0169-unconfirmed-metadata-is-discarded.md)).
