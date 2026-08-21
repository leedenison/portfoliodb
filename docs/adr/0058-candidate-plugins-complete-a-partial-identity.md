# Candidate plugins complete a partial identity

The candidate stage runs when what a source stated leaves the choice of listing open,
rather than when a source stated nothing at all. A broker upload that names an ISIN and
no venue reaches it; one that names a ticker and its MIC does not.

Gating instead on a posting having no identifiers whatever fits a stage whose job is to
read identifiers out of free text -- with an identifier in hand there is nothing left
to read. It does not fit a stage whose job is to complete an identity, and it excludes
the case the stage is most useful for. An IBKR QFX states a CUSIP or an ISIN, a trading
currency and no venue at all; under that gate it never reaches a plugin, so the venue
is never proposed, and OpenFIGI -- asked about an ISIN that maps to every listing of
the security worldwide -- chooses among them on security type and precedence.

## The line is the venue, and nothing else

An identity is complete when the source named where the instrument trades.

That is not an arbitrary field to single out. Everything else an upload might leave out
follows from the listing, and an identifier plugin fills it in from its own data at no
cost: a ticker qualified by its MIC yields the currency, the ISIN, the asset class and
the name. Nothing fills in the venue, because a bare ticker maps to every listing of
that symbol in the world and an ISIN maps to every venue the security trades on -- more
data does not narrow it, and choosing among the answers is the whole job.

So a `MIC_TICKER` carrying its MIC is complete and a bare one is not; an ISIN, CUSIP or
SEDOL alone is not. Three kinds of identifier are complete because they name the
instrument rather than a listing of it: a currency or an FX pair is the cash or FX
instrument entire; a contract symbol -- OCC, OPRA, FUT_OPT -- carries its own
underlying, expiry, right and strike and names its market by construction; and a FIGI
is a provider's key into the provider's own data, which a model asked to improve on
could only invent.

Currency deliberately does not count. A source that states a ticker and a currency has
named a market at best, and USD covers a dozen US venues -- the choice the stage exists
to make is still open.

## Only a broker upload is offered completion

An archive states one identifier per posting, chosen out of an identity the exporting
instance had already resolved. It is a pointer to that instrument, not a partial
description of it. Judging the pointer incomplete and paying a plugin to complete it
mistakes a reference for a description, and what came back would be tested against a
stated identifier that was never partial.

The bound is on completion, not on the stage. A posting an archive names no identifier
for is one the exporting instance never resolved either, and it reaches the candidate
plugins on its description exactly as it always has.

## Consequences

A proposal is still a thing to be tested and never evidence
([0057](0057-a-proposed-identifier-is-not-evidence.md)). Widening the gate widens the
population that has proposals attached, and every rule about what may be done with one
is unchanged.

Nor does it change what the database is asked. Both lookups still happen in the
pre-pass, and completion is considered only after both have missed -- a key the
database already answers is never paid for, whether it stated an identifier or a
description.

A stated identifier that resolution cannot confirm now costs a paid plugin call where
it used to cost nothing. That is the trade the stage is for, and it is bounded by the
pre-pass on both sides: the call happens once per resolution key rather than per
posting, and not at all for a key the database recognises. How often it pays for itself
is measured rather than assumed; see issue
[0134](../issues/0134-per-field-candidate-telemetry.md).

A guess in this position is validated rather than verified: an identifier plugin
answers that a proposed venue is a real listing of the security, not that it is the one
the posting traded on. Closing that gap is
[0059](0059-an-invented-identifier-round-trips.md).
