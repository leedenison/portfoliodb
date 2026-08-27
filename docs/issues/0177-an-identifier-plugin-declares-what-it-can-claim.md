---
status: open
title: An identifier plugin declares what it can claim
milestone: M24
---

adr/0065 keeps two surfaces apart: what a plugin declares it can claim, and what one
call recorded claiming. The record is built -- `Result.Identifiers` is what a call
returned and `Result.Filtered` is what it was strictly filtered on, graded equally
because a provider that answers at all has asserted the filtered value denotes the
security it described. Nothing declares anything.

The shape exists to copy. `AcceptableSecurityTypes` is a declaration in exactly the same
sense -- static, per-plugin, sitting beside precedence and config, and already deciding
whether a plugin is called at all. What no plugin says is which identifier types it can
answer with, which of them can arrive together, and which it strictly filters on.

0175 is what needs it. Whether a user-authoritative claim is stored owned by its supplier
or not stored at all turns on whether any enabled plugin could have adjudicated it, and
that question is answerable from the declarations alone, without auditing a single row.
Nothing else in the system can answer it: a plugin that never returns ISINs may still be
the only thing able to confirm one by filtering on it, so the returned values in the
database do not say what could have been asked.

## The unit is a set of types that arrive together

Not a flat set of types the plugin may return. The question is about an **association**
-- could anything have adjudicated that these two identifiers denote one security -- and
a plugin that maps a ticker to an ISIN and separately a ticker to a CUSIP has never
claimed the ISIN and the CUSIP are one thing. A flat set says it has.

That error runs the wrong way. Over-declaring makes 0175 read a claim as adjudicable,
find it uncorroborated, and drop it -- permanently, because the plugin that supposedly
could settle it never can. So the declaration is a list of groups the plugin may answer
with, where a group is the types that can arrive in one answer, and a type it strictly
filters on belongs to the group it was filtered alongside.

## What it is not

Not enforced, and not a gate on a merge. Nothing checks that a plugin returns what it
declared, and a provider changing its response shape drifts the two apart silently, so no
association becomes a fact on the strength of a declaration -- that still takes the record
of a call that made the claim. Why the bar is safe to set where 0175 sets it is in
adr/0065.

Not per-provider truth about identifiers either. Whether a type routinely reassigns its
values is a property of the type and is declared once for the vocabulary
(adr/0061); putting it in a provider's config would make it a matter of opinion.

## Beyond 0175

The declarations make the reachable claim graph computable before any data arrives, so
what an admin gains or loses by enabling a plugin is answerable in the admin UI rather
than by waiting to see. That is the third of adr/0065's uses and it lands on the surface
0066 builds rather than on a card of its own.

See adr/0065-a-plugin-declares-what-it-claims-a-call-records-what-it-claimed.md.
