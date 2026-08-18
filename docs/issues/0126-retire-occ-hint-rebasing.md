---
status: open
title: Retire OCC hint rebasing from identification
milestone: M21
dependencies: [0125]
---

`AdjustOCCForKnownSplits` exists to make an OCC hint of one vintage match a
stored row of another. Once both names are stored with their intervals the hint
matches by value, so the rebasing, the strike arithmetic it carries and the
vintage it reports back for stamping all have nothing left to do.

Retire it against the stored intervals rather than alongside them: the two have
to agree about which contract a name denotes, and
[0036](../adr/0036-expired-options-are-not-restated.md) records what happens when
only one of the pair moves.

What does not go is disambiguating a value two instruments have held over
disjoint intervals, which is [0122](0122-resolve-identity-as-of-a-date.md)
rather than this.
