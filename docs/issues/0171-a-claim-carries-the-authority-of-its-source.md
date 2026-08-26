---
status: open
title: A claim carries the authority of its source
milestone: M24
dependencies: [0142]
---

Nothing records the level of authority a claim arrived with. `db.IdentityClaim`
drops attribution deliberately and rightly -- every identifier plugin is equally
authoritative for a global identifier, so which one answered decides nothing --
but the level is a different question, and it is the one that decides what a
merge may do. `mayMerge` stands in for it with `const systemOwned = true`, which
holds only while every claim reaching the merge site comes from a plugin result
or an admin's archive.

That stops holding when a broker file states an association of its own: 0143's
cross-domain claim, and 0123's contract identifiers before it. Both arrive with
user authority through a channel nobody can re-interrogate, and both are exactly
the claims a merge must treat differently.

## What to do

`IdentityClaim` and the identifier inputs an upload carries state the authority
they arrived with -- system, or user with an owner. Ingestion states user
authority; resolution and archive import state system authority. Merge admission
then reads two things, the claim's authority and each endpoint row's owner, and
answers with one of three outcomes rather than a boolean: merge outright, repoint
what is only claimed (0172), or refuse.

Not which source. The level is the whole of the question, and recording more
would invite a rule that reads it.

See adr/0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md and
adr/0079-an-instrument-carries-the-authority-of-the-source-that-named-it.md.
