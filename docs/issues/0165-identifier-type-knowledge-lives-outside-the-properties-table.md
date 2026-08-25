---
status: closed
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

## Outcome

Every question the rules ask of an identifier type is now answered from `Props`,
under the drift test the table was built for.

**Two properties were escaping.** `Domain` says what a type's domain does --
scope the value, name something beside it, or nothing, the type carrying none --
which was prose that already existed, misfiled in `Grain`'s doc comment. `Lines`
says how many of a security's currency lines a value reaches, and is not grain
restated: a contract symbol is security-grain and still reaches one line, a
contract being cleared in one place, where an ISIN reaches every line the
security trades in. `identifier.ReachesOneLine` reads the type from the table and
the domain from the value; `ingestion.statedIdentityComplete` is that asked of a
set, renamed so the pair is not two names one word apart.

`TestPropsCoversProtoVocabulary` gained four assertions: both properties declared
for every entry, and two invariants -- a security-grain type has no domain that
scopes, and a listing-grain type reaches one line. The second is what makes the
silent false this issue opens with impossible.

**One answer moved.** `OPENFIGI_SHARE_CLASS` was complete on the strength of a
sentence written as "a FIGI is a provider's key into the provider's own data",
one claim covering the share class and composite FIGIs together, before grain was
drawn per currency line. adr/0068 made the composite listing-grain, where it is
complete because it names a market whose venues share a currency; the share class
FIGI kept the sentence and lost the half doing the work. It reaches every line of
its class exactly as an ISIN reaches every line of the security, and now says so.
adr/0058's "a FIGI is a provider's key" clause is left as written: it was true of
the pair it was written about, and 0068 is what narrowed it.

**Derivative-ness was already declared.** 0162 made `db.IsDerivative` the one
definition and converted all four Go and TypeScript sites, so that bullet was
overtaken before this issue was read. What survived was SQL. The corporate event
query now binds `db.DerivativeClasses` rather than spelling the classes out, and
`chk_underlying_required` -- which cannot call Go -- is held in step by a test
reading the constraint back out of the catalogue, in the pattern
`TestAssetClassCheck_matchesProtoVocabulary` and `TestCurrencyFamily_matchesGoTable`
follow.

**One rule, two tables.** `NamesAListing` and `ProviderNamesAListing` reached the
same no by opposite-sounding arguments and nothing said they were one mechanic.
Both now go through `namesAListing`: a grain nobody declared is not a listing.

**Found and not fixed here.** The spec and adr/0058 both say a currency hint
completes an identity, so an upload stating an ISIN and a trading currency does
not reach the candidate stage. The code does not do this -- `tx.currency` becomes
`identifier.Hints.Currency` and never a `CURRENCY` identifier, so the gate never
sees it. That is a behaviour bug rather than a knowledge-location one, and is
0167.
