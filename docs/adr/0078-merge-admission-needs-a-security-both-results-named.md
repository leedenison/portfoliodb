# Merge admission needs a security both results named

A resolution calls every enabled identifier plugin on one identity and keeps the
winner's answer. A losing result that is admitted contributes its identifiers to
the security the winner resolved and fills the fields the winner left blank.
Admission asked two things: whether the two described one line, which the
currency decides ([0068](0068-a-listing-is-a-currency-of-a-security.md)), and
whether their identifiers contradict each other on a subject they share.

Neither is a question about the security, and where the two results share no
identifier subject the second asks nothing at all. So a loser is admitted on the
strength of a currency, and the ISIN it carries is written against a security it
may never have been about.

**A losing result is admitted only where something named the security both
results describe: one identifier of security grain that each of them named,
by returning it or by strictly filtering on it
([0060](0060-an-identity-claim-is-admitted-by-the-authority-for-its-scope.md)).**
Where nothing does, the loser contributes nothing -- no identifiers of either
grain, no filled fields, no claim.

The question arises because the identity a resolution starts from is routinely
ambiguous. A broker file names a symbol and a currency and no venue, and one
symbol is quoted in one currency in more than one place. Where two providers can
disagree at all, each has picked one listing out of several the query admits, and
that they agree about the currency is the query restated rather than evidence
they picked the same security. Only a value that names a security carries that
evidence, and only where both of them named it.

Grain selects the type, and it is already declared. A ticker, a SEDOL and a
composite FIGI name one line, and two results agreeing about a line have not said
they resolved one security -- 0060 says exactly this of a currency and a venue. A
broker description is excluded on the other axis: two securities can wear one
description, so agreeing on the text is agreeing about the text rather than about
its subject. Routine reassignment does not exclude a type here, though it bars
one from mediating a chain
([0061](0061-transitivity-needs-a-non-reassigned-identifier.md)): a contract
symbol passes to another strike over time, but two results resolving now from one
symbol both mean today's contract. Reassignment is a question about time, and
this one is about how much a query left open at an instant.

## Considered options

**The name, or the CIK.** Both name the security rather than the line, and
neither is an identity signal. Providers do not spell one security's name alike
-- OpenFIGI falls back to a description and then to the bare ticker -- so a
comparison strict enough to catch two securities rejects two spellings of one,
and one loose enough to accept the spellings catches nothing. A CIK names the
issuer, so two share classes share one and a difference marks a group boundary
rather than a security boundary.

**The venue.** This is what merge admission used to compare, always, and
issue [0159](../issues/0159-merge-admission-tells-two-lines-apart-by-currency.md)
removed it because it rejected lines 0068 says are one: LSE IOB dollar lines of
US stocks, euro lines across XETR and XPAR, interlisted dollar lines on XNYS and
XTSE. It caught some of what this catches, by accident of geography, and missed
every case where two securities trade in one country.

## Consequences

**What 0159 admitted is narrowed rather than restored.** Two results naming two
venues and one currency still describe one line, and that finding stands: the
venue does not tell two lines apart. What no longer follows from it is that the
loser's answer may be filed against the winner's security. Where the two also
name one security in common, the merge proceeds exactly as 0159 left it.

**Two plugins that share no security-grain vocabulary do not merge.** EODHD
returns an ISIN and a `MIC_TICKER`; OpenFIGI returns a share class FIGI, a
composite FIGI and an `OPENFIGI_TICKER`; the two never meet. So a resolution
starting from a bare ticker or a broker description keeps the winner's answer
alone. It merges where a source stated a security identifier, because OpenFIGI
filters on it and EODHD returns it, and OpenFIGI and Massive merge on the share
class FIGI they both return. The remedy for the rest is a plugin returning more
identifier types, not a weaker test.

**An absence is recorded as itself.** A refused loser's call is
`discarded_uncorroborated` rather than `discarded_inconsistent`. Nothing
contradicted it; the two answered an ambiguous query and nothing tied their
answers together, which is an ordinary outcome rather than a disagreement. A
reader asking why a plugin's answer did not reach an instrument needs to know
which of the two happened.
