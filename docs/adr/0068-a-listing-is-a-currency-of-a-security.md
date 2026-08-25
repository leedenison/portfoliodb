---
status: partly superseded by ADR-0075
---

# A listing is a currency of a security

A security listed in two currencies -- the GBP and USD lines of one iShares ETC
-- may carry one ISIN, and one row per instrument cannot hold both. The eager
merge in [0004](0004-instrument-resolution-and-merge.md) combines them, prices
arrive from whichever line the plugin picked, and the portfolio is wrong by an
FX rate. An instrument is therefore split into a **security** and its
**listings**, and a listing is one currency the security trades in.

Venue is an attribute of a listing rather than part of its identity, so two
listings of one security are required to hold different currencies. Two venues
quoting one security in one currency differ by a spread; two currencies differ
by an FX rate, and only the second makes two holdings non-fungible. Keying on
the venue would distinguish what a portfolio may safely add together while
costing the thing that actually matters.

## Considered options

**Keying a listing on the venue, or on the pair.** Rejected on the two grounds
above and on a third: the discriminator has to be available when a posting is
written, before any plugin runs
([0072](0072-a-posting-names-a-security-and-a-line.md)). A broker routinely omits
the venue and routinely states a currency, so a venue-keyed listing would be
unknown on ingest for almost every row where a currency-keyed one is not.

## Venues

A listing's venues are derived from its listing-grain identifiers into
`listing_venues`, maintained by trigger in the pattern
`recompute_instrument_name` already follows, which keeps a real foreign key to
`exchanges` while making divergence unrepresentable. This is what issue
[0099](../issues/0099-single-source-for-an-instruments-exchange.md) asked for,
and it supersedes that issue's premise that a security has one exchange. The
security's own `exchange_mic` is gone rather than derived, there being no venue
above the line to derive one from; how open the set it leaves behind is read is
[0077](0077-a-venue-set-is-what-we-know-not-what-exists.md).

A bare MIC does not always identify a listing, and the motivating case is where
it does not: the LSE lists both lines of that ETC. The full `MIC_TICKER` triple
does, the two lines carrying different tickers. A bare MIC matching two listings
of one security is unresolved and must never be settled by picking one.

## The currency family

Listing uniqueness is on a currency **family**, not on the raw ISO code: GBX and
GBP are one currency under a different unit prefix, which the codebase already
records as an exponent in `pricefetcher.DerivedFXPairs`. Without the family, one
provider quoting the London line in pence and another in pounds fork one line in
two.

The family governs the uniqueness index and nothing else. It never rewrites a
code -- not a `CURRENCY` identifier, not an `FX_PAIR`, not `trading_currency`,
not a stored price -- because GBX and GBP are separately seeded CASH
instruments, `GBX/USD` and `GBP/USD` separately seeded FX instruments, and
valuation compares a currency to the display currency directly. Normalising
codes would collapse instruments that are deliberately distinct.

## The unknown listing

Superseded by [0075](0075-a-name-that-could-not-be-placed-names-no-line.md). A
security whose currency is unknown carried a listing with a null currency, which
was not priceable and not event-bearing. It now holds no listing at all, and the
listing-grain names nobody could place name no line.

## Grain is re-declared, not inherited

`identifier.Grain` already names this axis, but it meant security against
*venue-listing* and now means security against *currency line*, so the table is
re-read rather than carried across. `OPENFIGI_COMPOSITE` moves to listing grain
-- its own comment, that a composite "names a security within a market rather
than one venue's line of it", is the argument, since a security within a market
is a currency line -- and `SEDOL` moves with it, being assigned per market and
per line. Both keep `ReassignRare` and so keep mediating an association, which
`MayMediate` decides from reassignment alone.

Grain also stops implying a domain. A ticker needs one to say which line it
names; a SEDOL and a composite FIGI are globally unique without one, as an ISIN
is at the level above. Grain says what a value names; whether a domain scopes it
is a separate property.

## Consequences

A composite exchange code becomes a complete answer rather than a gap: it names
a country's venues, which share a currency, so it names a listing exactly. For
the same reason a currency hint now completes an identity, which is the opposite
of what [0058](0058-candidate-plugins-complete-a-partial-identity.md) assumed,
and that stage narrows to sources stating no currency at all.

A venue migration stops being an event and becomes a change to a set.

The valuation shortcut that treats a null currency as the display currency is
withdrawn. It silently valued a holding at an FX rate of 1; under this decision
a null currency means the line is unknown and the holding is unpriced.
