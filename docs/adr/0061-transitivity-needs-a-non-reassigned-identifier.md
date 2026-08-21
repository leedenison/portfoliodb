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
broker-scoped identifiers was meant to prevent, arriving through the rule itself.

What matters is whether the mediating value can quietly denote two things:

> A chain is permitted through an association that is **system-owned**, whose identifier
> type does not **routinely** reassign its values, and whose two halves have
> **overlapping** validity intervals.

Three conditions, and each catches something the others do not.

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

## Ownership is a separate condition, and the easy thing to miss

A broker's contract identifier passes the type test outright: IBKR states that conids
are static per contract and never change. Reading only that, an implementor would chain
through one -- and reintroduce exactly the blast radius
[0062](0062-a-user-mediated-claim-is-a-lead-not-a-write.md) exists to stop, because the
only way a conid ever reaches this system is inside a file a user uploaded.

**A user-owned association mediates nothing.** Not even for its own user: the identifier
rows are owner-scoped but instruments are not, so a chain drawn through one merges
instance-global rows on the strength of one unauthenticated file. It becomes eligible
only once the promotion sweep has made it system-owned
([0063](0063-identity-claims-are-owned-until-users-corroborate-them.md)), or once an
identifier plugin has settled it as a hypothesis. Promotion and verification are two
routes to the same place.

## Consequences

- The type property alone is never sufficient. Every use of it has to ask about
  ownership in the same breath, and an implementation that reads the two from different
  places will eventually read only one.
- IBKR's wording is documentation rather than a formal commitment, and no volume of
  exports demonstrating stability is evidence about what has not happened yet. That is a
  further reason the ownership condition carries weight here: it is the only part of the
  test that rests on something we observed rather than on something a vendor wrote.
- The same check serves two call sites -- whether two instruments may merge, and whether
  a newly learned name may be written onto an instrument that already exists. One rule,
  asked twice.
