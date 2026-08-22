# Listings merge by currency and an unknown one splits

A security merge has to merge listing sets, and a security whose unknown listing
turns out to be several real ones has to divide one -- an operation nothing in
the system had.

Merging unions the listing sets keyed by currency family. Two listings of one
currency merge, taking the union of their venues, identifiers, prices and
dividends, and two unknown listings merge into one. Because currency is the key
rather than an attribute, a collision *is* a merge: there is no case where two
survivors have to coexist and nothing says which wins.
[0064](0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md) still applies
to a collision inside a merged listing's identifiers.

The split is possible because an unknown listing holds nothing that has to be
divided. It is not priceable and not event-bearing
([0068](0068-a-listing-is-a-currency-of-a-security.md)), so there are no prices,
no coverage rows, no dividends and no fetch blocks against it -- only postings,
and every posting already carries the trading currency that says which listing
it belongs to. The split is a relabelling rather than an apportionment: each
posting moves to the listing its own currency names, a posting stating no
currency stays, and an emptied unknown listing is deleted.

Under a venue-keyed listing this would not have worked. Prices would have
accumulated against whichever line the plugin picked, their currency unknowable
after the fact, and dividing them would have meant discarding them. Declining to
price an unknown listing at all is what turns a loss into a relabelling.

## Completion in place

An unknown listing that learns its currency is completed in place, as
[0067](0067-an-instrument-with-no-identity-is-completed-in-place.md) completes
an instrument holding no identity, and merges into any sibling already holding
that currency. Both refusals 0067 answers are answered the same way one level
down: nothing is replaced, because the column filled was null, and nothing is
associated that a source did not state, because the currency comes from the
postings' own evidence rather than from an identifier set the resolver
assembled.

## Consequences

A holding on an unknown listing is unvaluable rather than valued wrongly, and
surfaces as a repair. That is the visible cost of declining to guess a line, and
it is preferred to the alternative this whole change exists to remove: a holding
valued confidently against the wrong currency.
