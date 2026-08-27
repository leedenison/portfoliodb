# What may mediate a transitive association

An association can be corroborated without one source naming both ends. If an
identifier plugin returns a CUSIP with an ISIN, and another returns a SEDOL with the
same ISIN, the CUSIP and the SEDOL are linked through the ISIN and no third call is
needed. Chaining is worth having -- without it a security master stays fragmented
across providers that each know a different half of an identity, which is the
convergence [0004](0004-instrument-resolution-and-merge.md) exists for. The question is
only what may be chained through.

Scope is the wrong axis. `MIC_TICKER` is global by namespace and reused constantly, so
`ISIN-A` to `XNAS:EA` to `ISIN-B` is two global hops that chain Electronic Arts to
whatever holds the symbol now -- the failure a rule against chaining through
broker-scoped identifiers was meant to prevent, arriving through the rule itself. Scope
is the wrong axis for who vouched for a value as well
([0060](0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md)); this
ADR is about the value rather than about the source.

What matters is whether the mediating value can quietly denote two things:

> A chain is permitted through an association whose identifier type does not
> **routinely** reassign its values, whose two halves have **overlapping** validity
> intervals, and which the system holds as a **fact** rather than as a claim.

Three conditions, and each catches something the others do not. The first two are
questions about the identifier and are declared per type and per row; the third is a
question about the source that supplied the association, and is read from its owner.

## What the interval catches, and what it does not

The interval catches reassignment we know about. A CUSIP reassigned in 2019 is two
associations over disjoint intervals; an option restated by a split is two OCC symbols
over disjoint intervals; a security that changes ISIN on redomiciliation is two
associations over disjoint intervals
([0055](0055-identifier-validity-is-an-interval.md)). In each case the chain is refused
without anything having to know which case it was, and legitimate restatement stays out
of the error path.

It does not catch reassignment we never learned about, and that is the case the type
property is for. One plugin returns `{CUSIP-X, ISIN-1}` and another returns
`{CUSIP-X, SEDOL-1}` at a later vintage. If `CUSIP-X` was reassigned in between and
nobody told us, both associations are stored open-ended, both are true as of when they
were made, their intervals overlap, and the chain merges two unrelated securities. No
interval reasoning reaches that, because the bound that would have separated them was
never recorded.

Both associations there are facts an identifier plugin wrote, which is what makes this
condition worth keeping apart from the authority of the source. No question about who
said it reaches this failure. The value moved, and only a property of the value catches
that.

## Rarely is not never, and that is where the line falls

Demanding the type be *guaranteed* never reassigned disqualifies almost everything
worth chaining through: only a FIGI clears that bar, and refusing to chain through
ISINs would leave the master permanently fragmented for the sake of an event that
happens by documented national exception. That trades a frequent, certain cost against
a rare, correctable one -- and the rare error is correctable. We already accept that a
single identification pass cannot tell valid from correct, that a wrong merge therefore
spreads, and that the repair is a person with a surface to work from
([0064](0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md)).

So the line is between reassignment as an *exception* and reassignment as the *norm*:

- **Chain.** FIGI, retired and never reassigned. ISIN, CUSIP, SEDOL, CINS and
  WERTPAPIER, where reassignment is an exception rather than a practice.
- **Never chain.** Tickers, `MIC_TICKER` and `OPENFIGI_TICKER`, where reuse is routine
  and `EA` is a live example rather than a hypothetical. Contract symbols, `OCC` and its
  kin, for the same reason from a different direction: a forward split hands one
  contract's old symbol to the strike below it, and 0055 records that this is reachable
  on most splits rather than on unusual ones. `BROKER_DESCRIPTION`, which is not
  injective at all -- two securities can wear one description, so it fails before
  reassignment is reached.

The distinction is a judgement about frequency, so it is recorded as a declared property
of the type with the evidence beside it, revisable when the evidence changes, rather
than derived from the identifier's scope or shape.

## A claim mediates nothing, and that is the easy one to miss

A broker's contract identifier passes the type test outright: IBKR states that conids
are static per contract and never change. Reading only that, an implementor would chain
through one -- and reintroduce exactly the blast radius
[0062](0062-a-user-mediated-claim-is-not-a-write-to-shared-data.md) exists to stop, because the
only way a conid ever reaches this system is inside a file a user uploaded.

**An association the system holds as a claim mediates nothing.** Not even for the user
who supplied it: identifier rows are owner-scoped but instruments are not, so a chain
drawn through one merges instance-global rows on the strength of one unauthenticated
file. It becomes eligible when it becomes a fact -- once the promotion sweep has made it
system-owned ([0063](0063-identity-claims-are-owned-until-users-corroborate-them.md)),
or, where anything can adjudicate it at all, once an identifier plugin corroborates the
pair in the ordinary course of re-identification. Nothing tracks the claim between times
for either route to close: both simply find the association already stored when they next
run. For a broker's own contract number the second route does not exist, and promotion is
the whole of it.

Nothing about the identifier's type says which it is. The same `CONID`, the same
`ISIN`, is a fact from one channel and a claim from another
([0060](0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md)), so this
condition is read from the stored row's owner and never from the vocabulary.

## Consequences

- The type property answers one of the three questions and is silent on the other two.
  A predicate over the vocabulary can say whether a type routinely reassigns its
  values; it cannot say whether this row is a fact, because two rows of one type differ.
  The caller asks the row.
- All three are asked at one call site, so an implementation cannot satisfy the type
  test and forget the rest.
- IBKR's wording is documentation rather than a formal commitment, and no volume of
  exports demonstrating stability is evidence about what has not happened yet. That is a
  further reason the fact condition carries weight here: it is the only part of the
  test that rests on something we observed rather than on something a vendor wrote.
- The same check serves two call sites -- whether two instruments may merge, and whether
  a newly learned name may be written onto an instrument that already exists. One rule,
  asked twice.
