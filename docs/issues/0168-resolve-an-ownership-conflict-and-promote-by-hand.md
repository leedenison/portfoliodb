---
status: open
title: Resolve an ownership conflict and promote by hand
milestone: M21
dependencies: [0142, 0066]
---

0142's sweep promotes a mapping once enough users hold it and none contradicts
it. Where users do contradict each other it stops, and stopping is the whole of
what it does: both users' rows stay in place, each resolving for its own owner,
and nothing says the disagreement was seen.

Two things a person needs, and neither is a sweep's to do.

**Listing what users disagree about.** A conflict is a triple more than one user
holds with more than one answer. Listing it means naming the triple, the answers,
how many users hold each, and the ingestion source each arrived from -- a
systematically wrong converter reads as a cluster of these rather than as
scattered rows, and that is visible only when they are listed together.

**Resolving one.** Picking a winner deletes the rows that agree with it and
promotes the mapping to system-owned. It does **not** delete the rows that
disagree. A user whose file said otherwise keeps resolving to what their file
said, and the disagreement resurfaces on their next upload rather than being
settled by deleting their evidence.

**Promoting by hand.** The threshold validates the channel rather than the claim,
so a small number is the whole of the evidence available and an admin must be
able to promote below it -- on a single-user instance, at one.

This is a surface and the RPCs behind it, not a card of its own: it lands on the
one 0066 builds, beside the contradictions 0141 records and the repairs 0127
reaches for. What needs a person's attention is one question with one answer.

See adr/0063-identity-claims-are-owned-until-users-corroborate-them.md.
