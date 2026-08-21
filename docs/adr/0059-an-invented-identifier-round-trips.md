# An invented identifier round-trips before it is trusted

Where a source stated no identifier at all, the candidate plugin's proposal is the only
key resolution has, and it is queried as such. A result reached that way is kept only
if it agrees with something nobody guessed. Agreeing with nothing is not a near miss;
it is the absence of a test, and the guess is dropped in favour of a
broker-description-only instrument.

## Validity is not correctness

An identifier plugin can tell us a proposal is valid. It cannot tell us it is correct.
OpenFIGI's mapping call answers for any real code, so a plausible invented ISIN comes
back confirmed -- and a plausible invented ISIN is usually a real ISIN belonging to a
different security. A plausible invented ticker is usually another company's.

[0057](0057-a-proposed-identifier-is-not-evidence.md) reads a non-empty response as
evidence that the value was matched, and that remains right. What it is evidence *of*
is the narrow thing: this code names a security. The question resolution is asking is
whether it names *this* one, and no amount of querying the guess can answer it, because
every answer is about whatever the guess names.

So the check has to come from outside the guess. The transaction states a security
type; the source stated identifiers, where it stated any. Those are what a result has
to agree with.

## Why a blank is the better failure

The damage from a wrong attachment here is not a merge -- 0057 closes that path by
keeping proposals out of the identifier set. It is durability. Where nothing was
stated, resolution stores the `(source, description)` binding, which is canonical and
instance-global, and `FindInstrumentBySourceDescription` hits it on every later upload
without any plugin running again. A wrong answer there is not corrected by better
plugins, a better prompt or more data. It is simply never looked at again.

A broker-description-only instrument is strictly better: it is visible in the UI as an
unidentified holding, it is repairable by hand (issues
[0104](../issues/0104-attach-an-instrument-to-an-unresolved-row.md) and
[0127](../issues/0127-correct-a-misidentified-instrument.md)), and it does not
propagate.

## Where the rule does not apply

Only where the whole identity was guessed. A source that stated an identifier is not
subject to this, because there the proposal was never queried -- it ranked among the
listings the stated key produced and introduced none of its own. There is no invention
to round-trip: the security was found by something the source said.

That also settles a probe this looked like it should add. Resolving twice on that path
-- once with the proposal, once without -- and discarding the proposal when the two
differ would defeat the point of passing it. Changing which listing is chosen *is* the
proposal's job, and
[0058](0058-candidate-plugins-complete-a-partial-identity.md) exists to let it.

## Provenance has to survive to the point of the check

The check is only worth anything if a proposal cannot count as its own confirmation, so
ingestion hands the identifier plugins a proposal marked as one, on both paths.
`ResolveWithPlugins` makes the substitution itself, in one place: where nothing was
stated, the proposal becomes the key it queries and looks up, while the untouched
identity is what the scoring and the confirmation read. Passing proposals in the stated
field instead makes them indistinguishable from evidence at every point downstream, and
puts a result matching only a proposed ticker in the top tier of winner selection
rather than the middle one where 0057 says it belongs.

## A filter answered is a confirmation, not an echo

The field that does most of the work here is the currency the transaction states, and
it looks at first as though it cannot be used. The OpenFIGI plugin puts the currency
hint onto the instrument it returns, which reads exactly like a plugin echoing an
unverified hint back as canonical data -- the thing `server/identifier/doc.go` forbids.
Hint and result would then agree by construction, and counting that would make almost
every guess self-confirming.

It is not an echo, and the reason is the one 0057 gives about identifiers: the
confirmation is the response, not the value. OpenFIGI's mapping call filters on the
currency it is given rather than ignoring it. Measured against the live API:

| query | result |
| --- | --- |
| Apple's ISIN, no currency | 258 rows, venues worldwide |
| Apple's ISIN, currency USD | 59 rows, USD venues |
| Apple's ISIN, currency EUR | 56 rows, European venues |
| Apple's ISIN, currency JPY | `No identifier found` |
| Toyota's ISIN, currency JPY | 37 rows |
| Toyota's ISIN, currency NOK | `No identifier found` |

The Toyota rows are the control: JPY is a currency the filter handles perfectly well,
so an empty answer for Apple is the filter working rather than the code being rejected.
A non-empty response is therefore OpenFIGI asserting that this security has a listing
in this currency, and since every row in the response matched the filter, whichever row
is chosen is one. The currency is a real test of a guessed identifier rather than a
decoration: it comes off the transaction and not off the guess, so a ticker naming the
wrong company survives only if that company also trades in the currency the source
stated.

### What each provider actually does

The rule only works if it is applied per provider, so all three were measured rather
than assumed.

| provider | filters sent | filtering | field in the response | what the plugin records |
| --- | --- | --- | --- | --- |
| OpenFIGI | `currency`, `exchCode` | strict | `exchCode` yes, currency **no** | the hint currency, on the filter's authority |
| EODHD | `type`, `exchange`, `limit` | strict | `Exchange`, `Currency`, `Type`, `ISIN`, `Country` | the provider's own values |
| Massive | none -- a path lookup | n/a | `primary_exchange`, `currency_name`, `type`, both FIGIs | the provider's own values |

EODHD is strict on both filters it is given, but nothing depends on that, because it
returns the fields too and `bestMatch` re-checks the exchange and the type against the
returned values. Massive sends no filters at all, so the question does not arise --
though it is US-only, which is its own trap: asked about `SHEL` it answers with the New
York ADR in USD rather than the London ordinary, and it is the currency check that
catches that.

OpenFIGI is the only one where a filter is the sole evidence for a field, which is why
it is the only place a hint is recorded and the only behaviour that has to be pinned by
a test.

Two things follow. The plugin contract in `server/identifier/doc.go` is sharpened: a
plugin must not return a hint the provider said nothing about, but a value the provider
confirmed *by filtering on it* may be returned, and the plugin must say at the site
which request made it a confirmation. And the behaviour is pinned by an integration
test, because the argument collapses silently if OpenFIGI ever becomes permissive --
the check two layers up would go vacuous rather than fail, so something has to fail in
its place.

## Consequences

A description-only posting whose source states neither a currency nor a security type
nor anything else checkable can no longer resolve through a proposal, however good the
guess. That is the intended reading of "agreeing with nothing is the absence of a
test", and it is narrow in practice: every converter states a trading currency, and
states an asset class on the rows that carry a security.

`resolution_key.mismatch_detected` is text naming which probe disagreed rather than a
boolean. Two different findings sharing one flag cannot be told apart afterwards, so
neither can be counted. A result found and refused is its own outcome,
`proposal_unconfirmed`, distinct from `broker_description_only`: both end at the same
kind of instrument, and "an answer was found and not trusted" is not "nobody answered".
