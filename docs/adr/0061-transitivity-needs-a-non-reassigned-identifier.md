# Transitivity needs a non-reassigned identifier and overlapping intervals

An association can be corroborated without one source naming both ends. If an
identifier plugin returns a CUSIP with an ISIN, and another returns a SEDOL with
the same ISIN, the CUSIP and the SEDOL are linked through the ISIN and no third
call is needed. Chaining is worth having; the question is what may be chained
through.

The first answer tried was scope: chain through a global identifier, never
through a broker-scoped one or a description. It is the wrong axis. `MIC_TICKER`
is global by namespace and reused constantly, so `ISIN-A` to `XNAS:EA` to
`ISIN-B` is two global hops that chain Electronic Arts to whatever holds the
symbol now -- the failure
[0122](../issues/0122-resolve-identity-as-of-a-date.md) exists for, arriving
through the rule meant to prevent it.

What matters is not who issued the identifier but whether the value can denote
two things:

> A chain is permitted only through an identifier whose type is guaranteed never
> to be reassigned, and only where the two associations' validity intervals
> overlap.

## Why the interval condition is not a refinement

Without it the rule needs an exception list, because "never reassigned" is a
spectrum: a FIGI is retired and never reassigned, an ISIN is not to be re-used
with documented national exceptions, a CUSIP is cancelled and reassigned after a
delay. An exception list is a second thing to keep correct, and it says nothing
about the case that is not reassignment at all -- a name legitimately given up.

Both are the same shape once validity is on the name
([0055](0055-identifier-validity-is-an-interval.md)). A CUSIP reassigned in 2019
is two associations over disjoint intervals. An option restated by a split is two
OCC symbols over disjoint intervals. A security that changes ISIN on
redomiciliation is two associations over disjoint intervals. In every case the
chain is refused for the same reason and nothing has to know which case it was.

It also keeps legitimate restatement out of the error path.
`CONID-X` to `ISIN-1` and `CONID-X` to `ISIN-2` is a contradiction only when the
intervals overlap; disjoint, it is a corporate action doing what corporate
actions do. `ProcessPendingOptionSplits` already mints the new name at the
`ex_date` and closes the old, so where the event is known the intervals are
disjoint and nothing fires.

## Non-reassignment is a declaration, not a fact

Nothing can prove an identifier type is never reassigned. IBKR states that conids
"are static for each and every contract and will never change", which is
documentation rather than a formal commitment, and no volume of exports
demonstrating stability is evidence of what has not happened yet.

So it is recorded as a property we declare of a type, on the best evidence
available, alongside the evidence. That makes it revisable when a counterexample
turns up, which a property inferred from the identifier's scope would not have
been. It also makes the declaration the thing to argue with when a new identifier
type is added, rather than the code.

## Consequences

- The prohibition on chaining through a broker-scoped identifier is not a rule of
  its own. It falls out for a broker's internal *symbol*, which carries no such
  promise, and does not apply to a CONID, which does. What restricts a broker's
  claims is the channel it arrives through
  ([0062](0062-a-user-mediated-claim-is-a-lead-not-a-write.md)), not the
  identifier's scope.
- A broker description can never mediate a chain, and not because of
  reassignment: it is not injective, so two securities can wear one description
  and it fails before the question is reached.
- The same check serves two call sites -- deciding whether two instruments may
  merge, and deciding whether a newly learned name may be written onto an
  instrument that already exists. One rule, asked twice.
