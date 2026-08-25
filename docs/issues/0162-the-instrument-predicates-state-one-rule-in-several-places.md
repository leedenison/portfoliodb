---
status: closed
title: The instrument predicates state one rule in several places
milestone: M25
---

Several rules about instruments, lines and currencies are written out more than
once, and at least one pair of copies disagrees.

**Two `listingFor`s, with different answers.** `db/postgres/listings.go` and
`service/ingestion/listings.go` each define `listingFor`, each documented as the
line a currency names. They agree on a stated currency that matches a line, and
on no currency stated. They differ where a currency is stated and no line
matches it: the postgres one returns none, the ingestion one falls through to
the security's sole line -- so a posting stating GBP is placed on a security
whose only line is USD. Which of those is wanted is a design question and the
answer belongs in one function, not two.

`soleListing` is likewise defined in both files, and `SoleListing` in
`db/postgres/listings.go` re-implements the unexported `soleListing` three
functions above it with a different query rather than calling it.

**The currency family is compared in four places.** `currency.Family` is the
declared source, and then `identification.sameCurrency`,
`ingestion.listingInFamily`, the SQL `currency_family()` in the listing
uniqueness index, and inline `currency_family(x) = currency_family(y)` in four
queries each restate the comparison. `sameCurrency`'s comment says every currency
comparison in the package goes through it, which is true and covers one of the
four packages that compare currencies. `inflationfetcher.pluginAcceptsCurrency`
is a fifth, matching on `EqualFold` over a slice.

**"Is a derivative" is defined four times**, over four representations:
`identification.isDerivativeClass` on a string, `archiveimport.isDerivative` on
the proto enum, `openfigi/identifier.isDerivative` on a provider result, and
implicitly in `identifier.UnderlyingSecTypeHint`. Derivative-ness is not a
declared property of the asset class vocabulary anywhere, so each site spells
`OPTION or FUTURE` out again.

**"Identified" is defined on both sides of the wire**, as
`holdsNoCanonicalIdentifier` in SQL and `isIdentified` in the admin client.
Equivalent today, with nothing keeping them so.

Reduce each of these to one definition. The `listingFor` pair is the one with a
behavioural difference to settle first.

## Outcome

Each rule now has one definition, and one of the copies turned out to be a bug.

**`db.LineFor`** is the line-naming rule: a stated currency names the line in its
family, no currency stated names the sole line, and each rung refuses rather than
reaching past itself. The ingestion copy and its two helpers are gone, and the
postgres one loads the security's lines and asks it -- one query where the rungs
used to take two.

The two implementations had disagreed about a stated currency matching no line,
and the ingestion one was wrong: it fell through to the sole line, placing a
posting that stated GBP on a security's only line when that line was USD. What
made it survive is that the case was tested with two listings, where the rung it
falls through to cannot fire either, so the test passed under both behaviours.
docs/spec/postings.md already said a posting naming a currency its security has
no line for names no line; adr/0072 described the ladder without the guard, and
now states it.

**`currency.Same`** is the only currency comparison in the Go code.
`identification.sameCurrency` is gone. The SQL `currency_family` stays a separate
implementation for the reason it always was -- an index expression must be
IMMUTABLE -- and its lockstep test is unchanged.

**`db.IsDerivative`** is the only statement of which asset classes require an
underlying line. The four sites call it, converting from the proto enum or a
provider vocabulary first where that is what they hold.

**`db.Identified`** is the only statement of what identified means, and the API
carries it as a derived field. The client's copy is gone. The store still asks
the question in SQL, because the resolution path reaches it holding a UUID and no
row; `TestIdentifiedMatchesTheStore` holds the two in step, in the pattern the
currency family test follows.
