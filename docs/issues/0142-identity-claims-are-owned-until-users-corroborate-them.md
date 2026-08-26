---
status: open
title: Identity claims are owned until users corroborate them
milestone: M24
dependencies: [0139]
---

A broker is the only possible authority for its own contract identifiers and
descriptions, and the file carrying them reaches us through a user:
unauthenticated, single-shot, impossible to re-interrogate. Instruments are
instance-global, so a mapping learned from one upload rewrites reference data
for everyone. The trust is forced; what bounds it is scope.

`instrument_identifiers` gains an owner, null meaning system-owned. A
broker-scoped identifier or a broker-description association arriving in a
regular user's transaction upload is written owned by that user, and every
identifier lookup becomes owner-scoped with a system fallback. That is the
hottest path in ingestion and the cost is paid on every row, which makes this
the largest item in M24.

## The exclusion constraint has to change first

`excl_instrument_identifiers_overlap` keys on `(identifier_type,
COALESCE(domain,''), value, daterange)` with no owner, so two users holding
*conflicting* mappings for one triple is rejected at insert. That is the case
this issue routes to an admin, and under the constraint as it stands the
disagreement never exists to be surfaced. The owner has to enter the constraint
before any of the rest of this works.

It weakens an invariant deliberately. "One name denotes one instrument at a time"
becomes "one name per owner at a time", with system rows as the shared case. The
second-order effect wants writing down rather than discovering: a user-owned row
and a system row for one triple no longer collide, so a user can hold a mapping
that contradicts the instance's, and since lookups resolve owner-first their
transactions follow their own file. That is the user override the spec already
describes, arriving by a new route.

## Promotion

A periodic sweep promotes a mapping to system-owned once a configurable number
of users hold it with no user holding a conflicting one, deleting the rows it
was promoted from. Where users hold conflicting mappings it promotes nothing and
leaves both rows in place, each resolving for its own owner. Listing those
conflicts and resolving one is 0168, which lands on the surface M21 builds
rather than on a card of this issue's own.

The threshold validates the channel rather than the claim -- users all read the
same mapping out of the same broker security master, so their agreement rules
out a doctored, stale or misattributed file and says nothing about whether the
broker is right. It must therefore be allowed to be one, or the single-user
instance this first ships to never promotes anything. At one the sweep promotes
whatever a lone user's file said and no person is waited on, which is what lets
the admin surface follow rather than gate this.

Scope is transaction uploads, meaning broker files and the postings of a user
archive. Archives need nothing: a `UserArchive` has no instrument part and
importing a `SystemArchive` is admin-only, so the boundary is already in the
message shape.

See adr/0062-a-user-mediated-claim-is-a-lead-not-a-write.md and
adr/0063-identity-claims-are-owned-until-users-corroborate-them.md.

Merge admission reads the column, so it lands with it rather than after.
`mayMerge` passes `systemOwned` as a constant today because every stored row is a
fact in fact; once a row can be a claim, that constant is a defect.

See adr/0079-an-instrument-carries-the-authority-of-the-source-that-named-it.md.
