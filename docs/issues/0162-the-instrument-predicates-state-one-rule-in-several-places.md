---
status: open
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
