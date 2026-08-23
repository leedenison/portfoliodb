# An option's underlying is the line its strike is quoted in

A contract's strike is a price, and a price is in a currency. An option on
Rheinmetall struck at 480 is struck at 480 euros; one on Apple struck at 150 is
struck at 150 dollars. `instruments.underlying_id` named the security, which
left the strike denominated in nothing the row states -- readable only by
guessing at the security's currency, which is exactly the guess
[0068](0068-a-listing-is-a-currency-of-a-security.md) removed. The column becomes
`underlying_listing_id`, and the line it names is the one the strike is quoted
in.

That makes the currency agreement between a contract and its underlying a check
rather than a question. OCC, OPRA and FUT_OPT stay security-grain identifiers: a
contract is its own security, cleared in one place, and the symbol carries no
venue. What moves is what the contract is *written on*, not what names it.

## Which line

The currency is taken from the contract, not from the underlying:

1. **What the contract states.** For a transaction this is `trading_currency`,
   which reaches identification as a hint; for a resolution it is the currency
   the result named. A stated currency outranks everything below, being what a
   source said about this contract.
2. **What its symbology implies.** A contract wearing an `OCC` or `OPRA` symbol
   is cleared in the US options market and struck in dollars. That is a property
   of the vocabulary rather than of any one symbol, which is what makes it safe
   to read off the identifier type. `FUT_OPT` implies nothing: futures options
   are listed worldwide and the symbol does not say where.

The line is then the underlying's listing in that currency's family, **minted if
the underlying does not already have one**. A corroborated contract asserts its
own strike currency, and a strike is quoted against shares, so it asserts that
the underlying has a line there. An OCC symbol a source stated and a plugin
answered about carries a corroborated USD strike as surely as if the source had
spelled it out.

That is an assertion by a source rather than a guess, which is what admits it
under the rule [0072](0072-a-posting-names-a-security-and-a-line.md) sets for
postings: a line is minted where something asserts one and nowhere else. It holds
whatever lines the underlying is already known to have -- a security already
quoted in GBP gains a USD line rather than refusing the contract, because a
dollar-struck contract on it is evidence that the USD line exists and not
evidence that the contract is wrong. Where the underlying's only line is the
currency-unknown one, minting is the relabelling
[0068](0068-a-listing-is-a-currency-of-a-security.md) already describes, and
nothing is lost.

An identity nobody corroborated has no such authority. A whole-identity proposal
that agreed with nothing the source stated is dropped as a guess before this
question is reached ([0059](0059-an-invented-identifier-round-trips.md)), so it
never mints anything.

Evidence for the two rungs, from an IBKR OFX statement: the `OPTINFO` block for
`P RHM 20250919 480 M` carries `OPTTYPE`, `STRIKEPRICE`, `DTEXPIRE` and
`SHPERCTRCT` and no currency at all, while the option's own `SELLOPT` transaction
carries `<CURRENCY><CURSYM>EUR</CURSYM></CURRENCY>` against `USD` on the same
statement's US trades. So a broker does state it, on the transaction rather than
on the security master, which is the first rung. The same file's OCC-symbolled
US options are the second.

## A contract that declares no currency is refused

Because a declared currency mints, the only contract left without a line is one
that declares none: nothing states a currency and its symbology implies none --
a `FUT_OPT`, or a broker file that omits both. That contract is not stored as a
derivative. This is the same refusal that already applied when the underlying
could not be resolved at all, and it degrades the same way: the transaction path
falls back to a broker-description-only instrument, which is visible in the UI
and repairable, and the archive and price-import paths report a row-level
validation error. One contract nobody can place is not a reason to reject the
statement it arrived in.

Storing it anyway would mean a strike denominated in nothing, which every later
reader has to guess at -- and a wrong guess is silent, because a strike is a
plausible number in any currency.
[0068](0068-a-listing-is-a-currency-of-a-security.md) already says an unknown
listing is not priceable and not event-bearing; this is that claim reaching the
contract written on it.

Two alternative rungs were rejected. Taking whichever line the underlying's own
resolution happened to land on makes the answer depend on how the underlying was
found rather than on what the contract is. Taking the underlying's sole line
where it has one treats "there is only one" as evidence that the contract is
struck in its currency, which it is not -- and it is the rung that would quietly
put a euro-struck strike against a dollar line.

It is recorded in telemetry as `underlying_line_unknown`, distinct from
`not_identified` and from `proposal_unconfirmed`: the identity was found and is
not in doubt, and what is missing is the currency of the deliverable.

## Consequences

`split_factor_at` climbs from the option's underlying listing to the security
above it, a split being an action on the security that every line splits with.
`ListPendingOptionSplits`, `HeldEventBearingInstruments` and
`InstrumentsWithSplits` take the same join. No derived split row is written on a
derivative, which is unchanged.

An instrument merge repoints its loser's derivatives over the same line pairing
the postings and dividends use. Where the loser's line has no match on the
survivor the merge fails on the foreign key rather than repointing the contract
at a line in another currency, which would put the strike in the wrong
denomination. [0071](0071-listings-merge-by-currency-and-an-unknown-one-splits.md)'s
union of listing sets removes that case.

The API carries both: `underlying_listing_id`, which is stored, and
`underlying_id`, derived from it for callers that want the underlying without
caring which of its lines the contract is written on. Saying which line the UI
means is issue 0154.

An archive names a derivative's underlying by the line it delivers -- an
identifier and a currency -- so a file states what the ladder above would
otherwise have to re-derive, and a reference that names no line is reported
rather than guessed at.
