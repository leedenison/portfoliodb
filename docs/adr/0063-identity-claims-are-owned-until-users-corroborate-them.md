# Identity claims are owned until users corroborate them

[0062](0062-a-user-mediated-claim-is-not-a-write-to-shared-data.md) leaves a broker
trusted for its own contract identifiers and descriptions because nothing else
can supply them, while the channel carrying that trust is a file a user handed
us. The bound on that is ownership: a claim arriving through a regular user is
**owned by that user** until other users agree with it.

`instrument_identifiers` gains an owner. It is where the fact-or-claim
distinction of
[0060](0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md) is
recorded: null means system-owned -- a fact, written by a system-authoritative
source or corroborated by one -- which is what an identifier plugin writes and
what everything reads today. A broker-scoped
identifier or a broker-description association from a regular user's upload is
written owned by them, and resolves for them alone -- lookups are owner-scoped
with a system fallback.

## Promotion

A periodic sweep counts the users holding each user-owned mapping. Where the
count reaches a threshold the admin configures and no user holds a conflicting
mapping, the mapping is promoted to system-owned and the user rows it was
promoted from are deleted. Where users conflict, the mapping is listed for an
admin to resolve; resolving it deletes the user rows agreeing with the winner and
**leaves the losing rows in place**, so a user whose file said something else
keeps working and the disagreement resurfaces rather than being decided by
deletion.

## What the threshold measures

Not the claim. Every user reads `CONID-X is CUSIP-1` out of the same IBKR
security master, so ten users agreeing is one source seen ten times, and it says
nothing more about whether IBKR is right than one user does.

What it rules out is the file being doctored, stale, or from the wrong account --
faults of the channel, which is exactly what 0062 identified as the risk. That is
worth being explicit about, because the natural assumption is that a larger
threshold buys a more trustworthy mapping, and it does not. Two or three is the
whole of the evidence available; a larger number only delays promotion.

It follows that the threshold must be allowed to be one, and that an admin must
be able to promote by hand independently of the sweep. The instance this ships to
has one user, where any threshold above one means nothing is ever promoted and
the mechanism is inert exactly where it first runs.

## An admin's archive, and an admin's broker file

An archive uploaded by an admin is authoritative at every level, including its
instrument data. A broker transaction file uploaded by an admin is treated
exactly as a regular user's.

The distinction is not about the person. It is about the artefact and the act.
An archive is our own format, produced by an export that validated what it wrote,
so importing it is re-entry of data this system already stood behind; a broker
file is a third-party artefact nobody has vetted. And an admin uploading a system
archive is doing deliberate curation, where the same admin uploading last
month's statement is doing the routine thing everyone does. The care taken
differs, so the trust should.

## The archive split already does half of this

Nothing needs adding for archives. `UserArchive` has no instrument part, so a
regular user cannot state instrument data in one at all, and importing a
`SystemArchive` is admin-only. The separation
([0033](0033-system-and-user-archives-are-separate.md)) put the boundary in the
message shape rather than in a check, which is why there is no rule here to
enforce.

So ownership governs what arrives through **transaction uploads** -- a broker
file, or the postings of a user archive, both of which state identifier hints and
neither of which carries instrument data. That is the whole of the surface.

## Consequences

- Every identifier lookup becomes owner-scoped with a system fallback, and that
  is the hottest path in ingestion. This is the largest cost in the change and it
  is paid on every row, not only on rows that carry a broker identifier.
- Restoring a user archive into an instance with no instruments already resolves
  its postings from scratch, which the archive format calls working as intended.
  Ownership does not change that: what it changes is how far a mapping learned
  from those postings travels.
- Two users disagreeing about a broker mapping and two identifier plugins
  disagreeing about an ISIN are the same shape of problem -- authorities that
  cannot both be right, needing a person. They belong on one surface, not two.
