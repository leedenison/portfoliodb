# A proposed identifier is not evidence

A candidate plugin may propose an identifier a source never stated -- a ticker
for a row that carried only a CUSIP, a venue for a bare ticker. Resolution holds
what a source stated apart from what a plugin proposed, and lets only the first
stand in for a confirmation.

A proposal never satisfies a database lookup, never raises the conflicting-hints
error, and is never written back as an identifier. What it may do is break a tie:
where several plugins answered and nothing a source said separates them,
agreeing with a proposal beats precedence alone.

## Why the split is structural

The provenance lives in the shape of the call -- `identifier.Identity{Stated,
Proposed}` -- rather than as a flag on `Identifier`. `Identifier` is also what
becomes `db.IdentifierInput` and gets stored, so a flag there fails open: every
producer and every store site would have to remember to clear it, and one missed
site persists a guess as canonical identity. A separate field cannot be
forgotten, because reaching a proposal requires naming it.

## Why a proposal may rank but not resolve

An identifier plugin answers about whatever security the value it is given
belongs to. Asked about a proposed ISIN, it answers about the security that ISIN
names -- which is the question resolution is trying to test, not one the plugin
can settle. So a proposal is not queried and not returned.

Ranking is different. A proposed venue chooses between listings a stated
identifier already produced; it cannot introduce a listing of its own. That is
the whole value of passing it: a bare ticker maps to every listing of that symbol
in the world, and without a venue the choice among them is arbitrary.

The tiers are ordered so a proposal never displaces a statement:

    agrees with something stated  >  agrees with something proposed  >  precedence

A contradicted proposal costs a result its place in the middle tier and nothing
more. It never removes a result from contention, because a guess being wrong says
nothing about the plugin that disagreed with it.

The middle tier also refuses a result that argues with the source. Matching a
proposal is not enough on its own: a result contradicting a stated currency would
otherwise be lifted over one that merely failed to confirm it, and a proposal
would have outranked a statement by the back door -- the tiers alone do not stop
that, because a stated hint nothing confirms leaves the top tier empty. The test
is weaker than the top tier's: contradicting nothing, rather than confirming
something.

## Barring the return does not blind us

The confirmation is the response, not the value. OpenFIGI's mapping call returns
zero results when the identifier did not match, so a non-empty response is itself
the evidence that the value was good -- which is why the plugin can decline to
append a matched ISIN or CUSIP to its output and still have told us. That
non-echo predates this and exists for a different reason: OpenFIGI may return
corrected values for those types.

What the signal does not carry is correctness. A non-empty response proves the
code exists and maps to a security, not that it maps to *this* one, because a
plausible invented ISIN is usually a real ISIN belonging to something else.
Closing that gap needs a check against something independently known, which is
[0132](../issues/0132-an-invented-identifier-round-trips.md), not this.

## Consequences

The eager merge in [0004](0004-instrument-resolution-and-merge.md) is driven by
the identifiers handed to `EnsureInstrument`. A proposal that cannot enter that
set cannot draw a second instrument into a merge, so no separate rule against
merging on a guess is needed -- barring the return is that rule.
