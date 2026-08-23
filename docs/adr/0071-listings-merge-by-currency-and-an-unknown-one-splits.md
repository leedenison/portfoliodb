---
status: partly superseded by ADR-0075
---

# Listings merge by currency and an unknown one splits

A security merge has to merge listing sets.

Merging unions the listing sets keyed by currency family. Two listings of one
currency merge, taking the union of their venues, identifiers, prices and
dividends. Because currency is the key rather than an attribute, a collision *is*
a merge: there is no case where two
survivors have to coexist and nothing says which wins.
[0064](0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md) still applies
to a collision inside a merged listing's identifiers.

A merge is the same shape one level up: it moves the loser's postings onto the
survivor's line of the same currency family, and onto no line where the survivor
has none to match, which is exactly what is true of them once the line they named
is gone.

## The split

Superseded by [0075](0075-a-name-that-could-not-be-placed-names-no-line.md),
which deletes the unknown listing rather than dividing it. There is no longer a
row to rename, and the names it used to hold name no line until a security has
exactly one for them to mean.

## Consequences

A holding on no line is unvaluable rather than valued wrongly, and surfaces as a
repair. That is the visible cost of declining to guess a line, and it is preferred
to the alternative this whole change exists to remove: a holding valued
confidently against the wrong currency.
