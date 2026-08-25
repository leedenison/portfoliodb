---
status: open
title: Merge admission has no security identity guard
milestone: M25
dependencies: [0159]
---

Merge admission asks whether two identifier plugin results described one line,
and whether their identifiers contradict each other. Nothing asks whether they
described one security.

The two questions came apart in 0159. A winner naming a market and a currency now
admits a loser naming a foreign venue and the same currency, because under
adr/0068 that is one line -- and where the two share no identifier type, nothing
else looks. The loser's ISIN reaches the security on the strength of a currency
they happen to have in common. The venue comparison used to catch a subset of
this, but it caught it by rejecting lines adr/0068 says are one, which is why it
no longer stands there.

Merging two *instruments* is unaffected: that needs an identity claim
(adr/0060). What is unguarded is what gets stored on the security a resolution
already landed on.

The field to guard on is one that names the security rather than the line -- a
name, a CIK, an ISIN whose subject the two share. Two results whose ISINs differ
have described two securities whatever their currencies agree on, and today the
identifier loop catches that only because an ISIN is one subject; a result that
returns a name and no security identifier is unguarded.
