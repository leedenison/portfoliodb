---
status: closed
title: Two currency lines of one security cannot be told apart
milestone: M25
---

A security listed on one venue in two currencies -- the GBP and USD lines of the
same iShares ETC, for instance -- may have one ISIN, and PortfolioDB has one
instrument per identifier.

## Motivation

Better hints do not help here. If both lines resolve to the same ISIN, the eager
merge in adr/0004 combines them, and a holding in one currency line becomes
indistinguishable from a holding in the other. Prices then come from whichever
line the price plugin picks, and a portfolio valued in the wrong currency line
is wrong by the FX rate rather than by a rounding error.

This is why the case is out of scope for the candidate work in 0131: it is not a
question of knowing more about the instrument but of the model having no place
to put a second listing.

## Scope

This issue was the decisions and the documentation, and nothing else. The code is
0146-0155.

Four ADRs:

- adr/0068 -- a listing is a currency of a security
- adr/0069 -- a listing is named by a security identifier and a currency
- adr/0070 -- a posting names a listing
- adr/0071 -- listings merge by currency and an unknown one splits

adr/0004, 0035, 0057, 0058 and 0067 are amended, and 0099 is closed as
superseded -- it argues for one authoritative exchange per instrument, which is
the premise this drops. Eleven spec files are revised: identifiers.md,
portfoliodb-spec.md, archive-format.md, prices.md, postings.md,
corporate-events.md, display-currency.md, performance.md, telemetry.md,
bitemporality.md, tx-types.md and information-architecture.md.

The cost the code stages carry, measured rather than guessed: 14 tables hold a
foreign key to `instruments`, 7 of them in a primary key including the
`eod_prices` hypertable; ~7,000 lines of database Go and 102 SQL literals; 3 of
the 5 plugin interfaces and their 30 cassettes; 24 of 52 RPCs and
`InstrumentRef` with them. The client is nearly untouched. The valuation query
is close to neutral -- almost everything it keys on `instrument_id` is already a
listing fact. P01 is the deadline, since proto and schema changes are free
pre-release.

## Decisions

**A listing is a currency of a security. Venue is an attribute of a listing,
not part of its identity** (adr/0068). Two venues quoting one security in one
currency differ by a spread, not by an FX rate, so instruments are fungible
across venues and distinguishing them is spurious. Two listings of one security
are required to hold different currencies. The word "listing" is kept because
`identifier.Grain` and docs/spec/identifiers.md already use it; the spec must
say plainly that it is keyed on currency and not on venue admission.

Whether the lines share an ISIN is answered: sometimes they do and sometimes
they do not, since legally distinct lines are assigned distinct ISINs. Both
cases suit the same grain -- a shared ISIN is one security with two listings,
distinct ISINs are two securities with one listing each.

### Knowing the security but not the line

The discriminator is present before identification. `txs.trading_currency`
already exists and a group's cash leg carries a currency regardless, so the
listing is decidable at ingest, before any plugin runs. That is what collapses
the same question asked in four places, and it is why the grain had to be the
currency: under a venue-keyed listing the line would have been unknown for most
rows at the moment the weight is written.

A posting always names a listing and never a security (adr/0070).
`weight_commodity` becomes `lst:<uuid>` beside the existing `cur:<code>` and
`desc:<text>`, so `check_tx_group_balance` never sees two grains and the
spurious residual does not arise. Holdings aggregate by listing with nothing to
net across grains. `transfer_matches` keeps its security-grain key and gains
`from_listing_id` and `to_listing_id`, which makes a cross-account transfer and
a currency-line conversion one object rather than two, both being
quantity-preserving on one security.

A posting's listing is resolved in this order: the stated `trading_currency`,
then the group's cash-leg currency, then a stated listing-grain identifier
including its ticker, then the security's sole listing, then the security's
currency-unknown listing.

`txs` carries `listing_id` and no security column. Portfolio filters and split
adjustment reach the security by joining `instrument_listings`. Two columns that
can disagree on the hottest table in the schema is the failure being removed one
level up, at the point where it is most expensive; if measurement later shows
the join is worth removing, the column returns derived by trigger, in the
pattern `listing_venues` uses, and never independently written.

A security whose currency is unknown carries a listing with a null currency, at
most one per security. That distinguishes the two things a single representation
could not be asked apart: exactly one line is a listing with a currency and no
siblings, and how many is unknown is the null row. **A currency-unknown listing
is never priceable and never event-bearing** -- a price with no currency states
nothing -- so the fetchers skip it and its holding reports unpriced.

### Naming a listing

A listing is named by a security identifier and a currency (adr/0069), so no
identifier is invented for one and adr/0059 is not invoked. `InstrumentRef`
gains an optional currency and `bestIdentifierJoin` splits into a security join
and a listing join, which is what removes the grain-mixing in its priority
order.

`BROKER_DESCRIPTION` stays security-grain, as `identifier.Grain` already has it,
because the transaction's currency supplies the listing. The worry that the
`(source, description)` mapping could not be stored where a broker's text does
not distinguish the lines does not arise.

### What names the security

`recompute_instrument_name` ranks `MIC_TICKER` first, and that type is now
listing-grain. The trigger reads both identifier tables, keeps the type
priority, and extends its tie-break to `(type priority, currency, domain,
value)` so the answer is stable across a security's listings, preferring a
listing that has a currency so that a security with a known line is never named
by its unknown one. It also starts firing on `instrument_listing_identifiers`
and on listing insert and delete.

No listing is primary. Naming one would reintroduce the default-versus-unknown
conflation this issue removes, bought for a display label; dropping tickers from
the priority instead would name most securities by a broker description or a
UUID.

### Merge, and the inverse it now has

Currency is the merge key rather than a collision axis (adr/0071). Merging two
securities unions their listing sets by currency family; two listings of one
currency merge, unioning venues, identifiers, prices and dividends; two unknown
listings merge into one. Two survivors colliding is not a case.

The split has an answer because the unknown listing holds nothing that must be
divided -- no prices, no coverage, no dividends, no fetch blocks. When a
security's unknown listing turns out to be several, each posting moves to the
listing its own trading currency names, postings with no currency stay, and an
emptied unknown listing is deleted. Under a venue-keyed listing the prices would
have had to be discarded; declining to price an unknown listing at all is what
turns that loss into a relabelling.

### An identifier lookup that lands on two securities

An ISIN resolving to one security while a `MIC_TICKER` resolves to a listing of
another is not a third outcome needing a home. It is the ordinary two-instrument
case, and the existing rules decide it: one plugin result naming both, or
filtering strictly on one, is a corroborated claim and merges them under
adr/0060, after which the listing sets union; a pair the resolver assembled from
two results is not a claim anybody made, so adr/0064 applies -- no merge, the
instrument degrades to broker-description-only, the contradiction is recorded.

What reads as new is not a conflict at all: a resolution may now return a
security from one identifier and a listing from another **of the same
security**. That is the normal path, and it is what makes a `MIC_TICKER` hint
pick the line.

### Which grain each fact keys on

Listing: `eod_prices`, `price_coverage`, `price_fetch_blocks`, `cash_dividends`,
`holding_declarations`, `instruments.underlying_id`.

Security: `stock_splits`, `corporate_event_coverage`,
`corporate_event_fetch_blocks`, `portfolio_filters`, `instruments.name`, and the
contract symbols (OCC, OPRA, FUT_OPT), a contract being cleared in one place.

`cash_dividends` is the one the schema had already half-said: it is keyed
`(instrument_id, ex_date)` and carries its own currency column, and that column
is the listing. A split is an action on the security, so `split_factor_at`,
`RecomputeSplitAdjustments` and `ApplyOptionSplit` take a security and reach its
listings. `underlying_id` names a listing because the deliverable is shares
denominated in a currency, which turns "an option's currency agrees with its
underlying's" from an open question into a check.

Price plugin eligibility resolves without a dilemma: `AcceptableCurrencies`
tests the listing's currency, `AcceptableExchanges` tests its venue set, and
`price_fetch_blocks` keys on the listing.

### Cash, FX, and the null-currency shortcut

A CASH instrument's single listing carries the currency the instrument carries
today, and every FX instrument's listing currency is USD under adr/0006's
pivot -- its quote currency, which is what a listing currency means. Both are
degenerate and both are coherent. GBX cash keeps a GBX listing; it is a
different instrument from GBP cash, so the currency-family index never sees them
together.

The valuation shortcut `COALESCE(inst.currency, $4)`, which treats a null
currency as the display currency, is withdrawn rather than carried over. It
values a holding at an implied FX rate of 1; under this model a null currency
means the line is unknown and the holding is unpriced. This moves valuation
totals for any non-cash instrument currently holding a null currency, so the
rows are counted before the change. Cash never reaches the branch, always
resolving through a `CURRENCY` identifier. The matching rule in
docs/spec/display-currency.md is retired.

### Grain is re-declared rather than inherited

`identifier.Grain` names this axis already, but it meant security against
venue-listing and now means security against currency line, so the table is
re-read rather than carried across -- porting it mechanically is the mistake
available here. `OPENFIGI_COMPOSITE` moves to listing grain, its own comment
being the argument: a composite "names a security within a market rather than
one venue's line of it", and a security within a market is a currency line.
`SEDOL` moves with it, being assigned per market and per line -- the GBP and USD
lines of an LSE ETC carry different SEDOLs. Both keep `ReassignRare` and so keep
mediating an association, which `MayMediate` decides from reassignment alone.

Grain also stops implying a domain. A ticker needs one to say which line it
names; a SEDOL and a composite FIGI are globally unique without one, as an ISIN
is at the level above.

### Rules that exist because the level is missing

Two collapse rather than being ported. The listing-level metadata rule in
docs/spec/identifiers.md (and 0144) becomes structure, because currency is no
longer a security column and there is nothing left to suppress; `consistentWith`,
`fillBlanks` and `confirmedFields` attach currency and venue to the listing the
result named. And a composite exchange code (0129) names a country's venues,
which share a currency, so it names a listing exactly -- partial knowledge
becomes total.

So the spec sentence saying a currency hint does not complete an identity, "the
choice among that market's venues" being what the candidate stage exists to
make, inverts. A currency hint is exactly what completes an identity, adr/0058's
stage shrinks to the rows with no currency at all, and adr/0057's proposal of a
venue for a bare ticker stops being needed. Measure the change in candidate
invocations against the baseline 0129 recorded.

adr/0003 stands and becomes less load-bearing: segments of one operating MIC are
on one listing anyway.

A bare venue does not always disambiguate, and the motivating case is where it
does not -- the LSE lists both lines of that ETC. What discriminates is the full
`MIC_TICKER` triple, the two lines carrying different tickers. A bare MIC
matching two listings of one security reads as unresolved, never as a pick.

### The currency family

GBX and GBP are one currency quoted in major and minor units, and the repository
already knows it: GBX is seeded as a CASH instrument, `GBXUSD` is derived from
`GBPUSD` at exponent -2, and OpenFIGI is sent `GBp`. Under a raw ISO-code key,
one provider quoting the LSE line in pence and another in pounds would fork one
line in two. Listing uniqueness is therefore on the currency family, with the
listing storing the code it is actually quoted in so that price magnitudes and
the existing scaling logic are untouched.

The family holds exactly one entry, `GBX -> GBP`, and grows on evidence. It is
the only minor-unit currency anything in the system handles: `DerivedFXPairs`
has one member and `openFIGICurrencyOverrides` has one. A list asserting others
would add entries nothing tests.

Those two maps and this one are three statements of the same fact, which is the
restatement adr/0066 collapsed once already. One table in a small `currency`
package -- code, major unit, exponent -- is the declaration; `DerivedFXPairs`
and the OpenFIGI override derive from it, and the SQL `currency_family` function
is held in lockstep by a test that reads it.

The family governs the uniqueness index and nothing else. It never rewrites a
code -- not a `CURRENCY` identifier, not an `FX_PAIR`, not `trading_currency`,
not a stored price -- because GBX and GBP are separately seeded CASH
instruments, `GBX/USD` and `GBP/USD` separately seeded FX instruments, and
valuation compares a currency to the display currency directly.

### Listings over time

GBX to GBP is consequently not a redenomination, and neither is a venue
migration an event -- it is a change to a set. What remains of listings over
time is a validity interval on the listing, a delisting that closes one, a real
redenomination that closes one and merges into another, and a broker converting
a holding between lines, which is quantity-preserving and so is a transfer.

`instruments.valid_from` and `instruments.valid_before` are deleted rather than
moved. They are dead: no query filters or orders on them, no plugin supplies
them -- the field exists on the plugin result type and nothing sets it -- the
client displays only identifier validity, and the sole non-test writer is
archive import, so a value enters from an archive we wrote and is echoed back
out. Nor is there a fact left for them to carry. When a name denoted the thing
is the identifier interval (adr/0055); when a line was tradable is the listing
interval, which is what the spec asks for when it says "available to trade on
the exchange"; when the security existed is the hull of its listings; and an
option's lifetime is `expiry`, which got its own column rather than reusing
`valid_before`. The proto fields go with them, renumbered from 1 with no gaps.

### The grain the user sees

`portfolio_filters` and `instruments.name` stay at the security, because a user
filtering on a company means the company. Holdings, declarations and prices show
the line, disclosed as a currency suffix -- "VOD (GBP)" -- which means something
to a user in a way a MIC does not. A holding on a currency-unknown listing is a
repair on the admin attention surface.
