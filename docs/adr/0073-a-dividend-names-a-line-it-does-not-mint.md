# A dividend names a line it does not mint

A dividend is paid in a currency, so it is a fact about one listing rather than
about the security above it
([0068](0068-a-listing-is-a-currency-of-a-security.md)). `cash_dividends` is
therefore keyed `(listing_id, ex_date)`. Keyed on the security it collides on its
primary key the first time one ex-date pays in two currencies, and the second
payment is lost.

The stated currency is how a row finds its line: the write path matches it
against the security's listings on the currency family, so a provider quoting the
London line's dividend in pence files against the line stored as GBP. **It never
mints one.** A dividend says a payment was made in a currency; that is not the
claim that the security trades in it. A broker converting a dividend into the
account currency and reporting it that way makes the first statement and not the
second, and a rule that minted from it would give the security a line it does not
have -- which then answers price fetches, holdings and merges as though it did.

This is the opposite trade from the one a posting makes. A posting's currency is
the broker saying what its figures are in for an instrument the user holds, and
[0072](0072-a-posting-names-a-security-and-a-line.md) mints from it. A dividend
arrives as reference data about a security the user may not hold, from a source
with no position to describe, and it arrives in bulk: a rule minting from it would
turn one provider's reporting convention into listings on every security it
covers.

## The dividend that names no line

A dividend whose currency matches no line is stored nowhere and goes to
`unhandled_corporate_events` as an `UNATTRIBUTABLE_DIVIDEND`, beside the special
dividends already queued there. It is not an error: the payment is real and the
file that carried it is well formed. What is missing is the one thing that would
let it be filed, and no rung recovers it -- picking the security's sole line
would file a converted amount against a currency it is not in, and the security
having exactly one line is not evidence that the payment was in that line's
currency.

Queuing rather than dropping is the same choice
[0064](0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md) makes for an
identity claim: a fact that cannot be represented is put in front of a person,
because the alternative is a silence nobody can distinguish from the provider
never having sent it.

## The currency column stays

`cash_dividends.currency` is kept rather than derived from the listing, because
it is the code the amount is quoted in and the family lets that differ from the
code the line is stored under. Nineteen pence is not nineteen pounds, and a
reader deriving the unit from a GBP line would read it as the second. This is the
same separation prices keep, where `ScaleBars` handles the exponent and the
stored code is left alone.

Agreement between the two is a property of the write path rather than of the
schema: the line is selected *from* the currency, so a row whose currency and
listing disagree cannot be produced by any writer. A cross-table constraint would
have to be a trigger, and it would have to compare families rather than codes to
admit the pence case -- which is the same comparison the write path already
makes, restated where it can drift.

## Consequences

An instrument merge moves the loser's dividends onto the survivor's line of the
same currency family, as it already moves the postings; without that they cascade
away with the loser's listings. The survivor's own row wins a collision, being
the one carrying the security's history.

`corporate_event_coverage` and `corporate_event_fetch_blocks` stay security-grain.
Coverage records that a provider was asked about a security over a date range,
and the fetch unit is the security: a provider answers about a ticker and returns
whatever currencies it pays in.
