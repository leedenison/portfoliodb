---
status: open
title: A user-owned association is repointed rather than merged
milestone: M24
dependencies: [0171]
---

The third of 0171's outcomes, and the only one with no code behind it today.

A user's broker file ties a description they hold to an instrument identified by
an ISIN. The claim carries user authority, so it cannot settle the association
for the instance, and the merge that a system-authoritative claim would perform
is refused. Refusing outright is wrong too: the user's own rows say the two are
one, and for that user they are.

## What to do

The user's identifier rows move onto the other instrument, keeping their owner,
along with the postings that resolved through them. Nothing on the target is
rewritten: its facts are untouched, its metadata is not merged with anything, and
no row changes owner. The instrument the rows left is not deleted -- another
user's rows may name it, and those users keep resolving there.

The metadata of the instrument the rows left does not travel. It is unconfirmed,
and the rule that would otherwise replace it applies only where an instrument
becomes system-authoritative under its own id (0169).

Where a fact would have to move instead, the claim is refused and the sweep of
0142 is what settles it later.

See adr/0079-an-instrument-carries-the-authority-of-the-source-that-named-it.md.
