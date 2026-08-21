---
status: open
title: A merge requires a corroborated association
milestone: M24
dependencies: [0139]
---

`EnsureInstrument` merges whenever the identifiers it is handed land on more
than one instrument, without asking whether anything said they belong together.

The union is what makes that unsafe. Consistency checking compares only the
identifier types present in **both** results, so two identifier plugins
returning disjoint types have nothing to disagree about, are consistent by
default, and have their identifiers combined on the strength of agreeing about a
currency and a venue. Two share classes on one venue in one currency satisfy
that, and so does an ADR beside its ordinary line.

A merge should act only on an identity claim some single result stated, or on a
chain through a third identifier whose type is declared never reassigned and
whose two associations have overlapping validity intervals. Metadata agreement
is checked and never merges.

The same rule governs writing a name onto an instrument that already exists,
which `updateInstrumentOnMatch` declines to do at all. Its comment justifies
that on vintage grounds -- matching is not evidence a stored name became correct
today -- which was right for a name already held and does not reach a new value,
since a new value carries its own `valid_from`. That restates **0136**: an
instrument accumulates what is authoritatively corroborated with a name it
already holds, rather than whatever resolution was passed.

Corroboration is a question asked of stored claims rather than a gate at write
time. A merge unjustified today becomes justified when a provider starts
returning a field it did not, or when an admin enables an identifier plugin or
pays for a richer tier, so the periodic re-identification already specified is
where it is asked again.

See adr/0060-an-identity-claim-is-admitted-by-the-authority-for-its-scope.md and
adr/0061-transitivity-needs-a-non-reassigned-identifier.md.
