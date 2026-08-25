---
status: closed
title: Merge admission tells two lines apart by currency
milestone: M25
dependencies: [0155]
---

`contradicts` treats two identifier-plugin results naming one symbol at two
venues as having named two listings, and `consistentWith` excludes the loser from
the merge on that basis. But a line is keyed on its currency
(adr/0068), so two venues quoting one currency are one listing and the venue
there stands in for the currency rather than deciding on its own account.

Merge admission is rightly strict -- it compares two answers rather than an
answer against a partial store, so the open-world venue rule
(adr/0077) does not reach it -- but it should be strict on the
field that decides identity. Two results agreeing on a currency and differing
only on venue have described one line.

Turned up while retiring the security's own exchange in 0155, which is where the
strict and permissive readings of a venue were separated and labelled.

## Outcome

The domain clause went rather than being made conditional. Every identifier
plugin sets a listing-grain domain from the venue it put on the answer -- eodhd
and massive from the venue's MIC, openfigi from the exchange code both the venue
and the `OPENFIGI_TICKER` domain come from -- and `SEDOL` and
`OPENFIGI_COMPOSITE` are emitted with no domain at all, so the clause only ever
fired on a ticker and only ever restated the venue comparison beside it. It
restated it worse: on a spelling, a composite's `US` against a venue's `XLON`,
that no country normalisation reaches. `contradicts` is deleted, being
`sameSubject` read negatively once the clause is gone.

`consistentWith` is two predicates rather than three arms, because it was asking
two questions through them. `lineMismatch` asks whether the two results described
one line, which the currency decides; `idMismatch` asks whether they contradict
each other about the security, which a value under a shared subject decides. The
venue lives inside the first as the stand-in for a currency nobody stated, which
is what adr/0077 meant by the venue standing in for the currency rather than
deciding on its own account -- so it fires in exactly the case the currency
leaves open rather than always.

Currency comparison moved to the family, in all four places that compare one:
merge admission, `CompareHints`, `confirmedFields` and `proposalOutcome`. The
database path already compared on family, so a source stating GBX contradicted on
the plugin path what it corroborated on the database path. `sameCurrency` is the
one place that fact is now stated.

`flattenClaims` came with it, found while checking that the admitted result's
identifiers actually reach the instrument. It kept one row per identifier type,
so a line's second venue was dropped -- and dropped whether it arrived from a
second result or from one result naming both, which trimmed a winner's own answer
on the way to the store. The venue set is derived from those rows (adr/0068), so
a venue went with each row lost. It keys on the subject now.

## Accepted consequence

A winner stating a market and a currency now admits a loser stating a foreign
venue and the same currency, even where the two share no identifier type -- so
that loser's ISIN reaches the security. The venue check that used to catch it was
doing security-identity work, and keeping it always-on would reject lines adr/0068
says are one: LSE IOB dollar lines of US stocks, euro lines across XETR and XPAR,
interlisted dollar lines on XNYS and XTSE. What two results merging *instruments*
requires is the identity claim (adr/0060), which is untouched; what loosened here
is what gets stored on the security. A guard on that belongs on a name, CIK or
ISIN disagreement and is issue 0160.
