---
status: open
title: Identifier type knowledge lives outside the properties table
milestone: M24
---

`server/identifier/idtype.go` opens by saying the vocabulary and the properties
the rules key off it are one table because a type in one and missing from the
other is the drift it exists to prevent. Three pieces of type knowledge have
escaped it.

**`ingestion.identityComplete` hardcodes a type list.** Its switch restates Grain
for SEDOL, OPENFIGI_COMPOSITE, MIC_TICKER and OPENFIGI_TICKER; adds an undeclared
fourth property -- whether a listing-grain type carries a domain that has to be
present before the value names a line; and adds a set of security-grain types
that are complete for reasons of their own. Add a listing-grain type to `idTypes`
and this returns false for it, silently.
`TestPropsCoversProtoVocabulary` guards the table against the proto enum and
cannot see this, and nothing else does either.

The fix is to declare the missing property -- what a type's domain does, whether
it scopes the value, names something beside it, or is absent -- and derive
`identityComplete` from `Props`. That folds the escaped knowledge back under the
existing drift test.

**Derivative-ness is not declared anywhere.** Four sites spell out `OPTION or
FUTURE` over four representations; see 0162.

**`providerIDTypes` is a second, parallel vocabulary.** It declares one of the
three properties, deliberately and with the reason recorded. It also means "does
this type name a listing" has two entry points whose out-of-table defaults are
opposites: `NamesAListing` returns false because an unknown domain must not be
read as a venue, `ProviderNamesAListing` returns false because an undeclared
provider type should file against the security. Both defaults are right; nothing
says they are two rules rather than one applied twice.
