---
status: closed
title: A claim carries the authority of its source
milestone: M24
dependencies: [0142]
---

Nothing records the level of authority a claim arrived with. `db.IdentityClaim`
drops attribution deliberately and rightly -- every identifier plugin is equally
authoritative for a global identifier, so which one answered decides nothing --
but the level is a different question, and it is the one that decides what a
merge may do.

0142 closed the stored half of it. An identifier row records who vouched for it,
and `mergeVerdict` reads that owner at both ends: a chain drawn through a row a
user owns is refused as `refused_unsettled`. What is left open is the claim in
flight. The merge site cannot recover the level afterwards -- a claim is not a
row and is never stored -- so it has to travel on the claim or not at all.

## It has already stopped being constant

The type's own comment says the level would carry nothing today, because every
claim reaching it comes from a plugin result or an admin's archive. That is no
longer true. `tx.identifier_hints` is a broker's own statement that two
identifiers denote one security, and it reaches resolution as
`identifier.Identity{Stated: ...}` on every ingestion path. It is used for
ranking and comparison and never treated as a claim, which is the only reason
nothing has gone wrong yet: were ingestion to state it, `mergeVerdict` would read
two system-owned rows, find nothing to object to, and merge two instruments on
one unauthenticated file. 0123's contract identifiers widen that channel rather
than opening it.

## What to do

`db.IdentityClaim` gains an `Owner`, mirroring `IdentifierInput.Owner`: empty is
system authority, a user id is that user's claim. Per claim rather than per call,
for the reason the row-level field is per row -- one ingestion resolution carries
both levels at once, the plugin's answers and the tx row's statement, and
`ensureSecurity`'s `owner` is who the resolution is *for* rather than who vouched
for any one claim.

Not which source. The level is the whole of the question, and recording more
would invite a rule that reads it. The owner rides along only because 0172's
repointing has to know whose rows it may move.

Ingestion states the uploader on a claim built from the stated hints; the
identification and archive-import sites state system. `mergeVerdict` then takes
the claim's level alongside the two endpoints it already reads, and refuses any
claim carrying user authority, under a refusal of its own beside the six there
are.

Bluntly, and on purpose. Refusing outright is wrong for the case where only the
user's own rows would move, but it is the safe end of the range and it is what
lets the producer land here rather than waiting: 0172 relaxes it to a repointing
where nothing on the target would be rewritten, and 0143 says what the refusal
does to the transaction that provoked it.

No proto and no migration. A claim is in flight only; the stored form of
authority is the owner column 0142 added.

## What this is not

`flattenClaims` decides which claimed identifiers are written, and once a
user-authority claim exists it must not flatten one into a system-owned input.
That is 0175's question -- what gets owned -- and this one is only about what the
merge site may act on.

See adr/0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md and
adr/0079-an-instrument-carries-the-authority-of-the-source-that-named-it.md.
