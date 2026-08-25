---
status: closed
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

## Outcome

The guard is a requirement rather than another field to compare. A losing result
is admitted only where each result named one identifier of security grain with
the same value -- returned by it, or strictly filtered on by the call, which
adr/0060 grades alike. Where nothing does, the loser contributes nothing: not its
identifiers, of either grain, and not the fields the winner left blank.

The field the issue asked for turned out not to exist. A name is not comparable
across providers -- OpenFIGI falls back to a description and then to the bare
ticker -- so a comparison strict enough to catch two securities rejects two
spellings of one. A CIK names the issuer, so two share classes share one. What is
left is the security-grain identifier, and where two results share none the
honest answer is that nothing tied them rather than that a weaker field should.

What makes that the right answer is what the query left open. A file names a
symbol and a currency and no venue, and one symbol is quoted in one currency in
more than one place, so where two providers can disagree at all each has picked
one listing out of several. Their agreeing about the currency is the query
restated. Recorded in adr/0078.

`CorroboratesSecurity` reads the grain and scope idtype.go already declares, so
no table was added: security grain, and not a description, which is not
injective. Routine reassignment does not exclude a contract symbol here, though
it bars one from mediating a chain -- reassignment is a question about time, and
this one is about an instant.

The refusal is `discarded_uncorroborated`, kept apart from
`discarded_inconsistent`: nothing contradicted the result, and a reader asking
why a plugin's answer did not reach an instrument needs to know which happened.

What 0159 admitted is narrowed and not restored. Two venues quoting one currency
are still one line; what no longer follows is that the loser's answer may be
filed against the winner's security.

EODHD and OpenFIGI share no security-grain vocabulary, so with today's plugin set
a resolution from a bare ticker or a broker description keeps the winner's answer
alone. It merges where a source stated a security identifier, and OpenFIGI and
Massive merge on the share class FIGI they both return. The remedy for the rest
is a plugin returning more identifier types, and is not this issue.
