---
status: open
title: A claim that would move a fact is refused and resolved to one instrument
milestone: M24
dependencies: [0171]
---

The third of 0171's outcomes, and the one whose answer had nowhere to go.

A user's file states that two identifiers denote one security. Both are held
system-owned, on two instruments. The claim carries user authority, so acting on it
would merge two instruments whose every association is already a fact -- settling an
association for the whole instance on the strength of one unauthenticated file. Nothing
in reach is the user's own, so 0172's repointing has nothing to work with either. The
merge is refused.

Nothing about that is broker-shaped. It was written as though it were -- a broker naming
its contract identifier beside a global one, and the chain drawn through it -- because
scope looked like the discriminator. adr/0079 has since put authority in its place, and
the same refusal falls out of a file naming an ISIN beside a CUSIP: it is any claim a
user-authoritative source makes over two facts, whatever the identifiers are called. The
broker case is left as the instance where the refusal is permanent rather than the reason
there is one, because no identifier plugin can be asked what a contract number means and
0142's sweep is the only route out.

The claim reaching the merge site through a user-owned row is a different refusal and
already has one: adr/0061's third condition, which `mergeVerdict` answers as
`refused_unsettled`. This is the case where both stored rows are facts and the caller is
the only thing carrying user authority.

## What the refusal does

The transaction still has to resolve, so this picks a winner rather than deferring. The
winner is the anchor -- the instrument the caller's highest-precedence identifier reached
-- which is already where a refused resolution attaches and already what the transaction
is filed against.

What changes is that the claim's identifiers which reached **no** instrument are written
on to the anchor, owned by the user, rather than dropped. They are dropped today because
carrying a name across would fail the exclusion constraint, and that reason only reaches
the names another instrument holds; one that reached nothing inserts under this owner
without colliding with anything.

Writing them is what makes the refusal repeatable. Nothing pins the winner today, so it
is re-derived from the caller's identifier order on every upload; once the user's own row
sits on the anchor, the next upload resolves through it and lands in the same place.

## The record

A `telemetry.merge` row with a refusal of its own beside the five there are, naming both
endpoints as whole triples. It is not a contradiction: two instruments nothing ever
joined are not a stored claim that they are different, so the accurate test is adr/0079's
-- whether acting would have to move a fact. A file naming a second CUSIP for a security
that already holds one is the other thing, and stays where 0141 put it.

## What this gives up

Two things, and neither is repaired later.

The user's holdings stay split across two instruments. The hypothesis this issue used to
raise had an identifier plugin to settle it; verification was the route out, and adr/0080
closed it by ruling that no functional path may read telemetry. The claim is re-derived
instead on every upload of the same statement, which settles it where a plugin can
eventually corroborate the pair and never where only a broker could.

And the winner keeps half of what the file said. The identifier is filed against one of
the two instruments, which 0142's sweep may in time promote to a fact, while the
association the file actually asserted -- that the two are one -- survives only as a
telemetry row nothing may read.

See adr/0079-an-instrument-carries-the-authority-of-the-source-that-named-it.md and
adr/0080-a-contradiction-is-logged-not-queued.md.
