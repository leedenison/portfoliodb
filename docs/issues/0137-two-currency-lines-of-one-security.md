---
status: open
title: Two currency lines of one security cannot be told apart
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

The unscheduled milestone item "Exchange and listing currency: identify and
store per transaction/instrument (and support multiple listings per instrument
if needed)" is this issue. The shape is a listing that carries the venue and the
currency, with the security above it and the transaction naming which listing it
traded on -- and every query that reads `instruments.exchange_mic` or joins
prices by instrument changes with it. 0099 is superseded rather than
neighboured: it argues for one authoritative exchange per instrument, which is
the premise this drops.

Whether the lines share an ISIN is answered: sometimes they do and sometimes
they do not, since legally distinct lines are assigned distinct ISINs. Both
cases suit the same grain -- a shared ISIN is one security with two listings,
distinct ISINs are two securities with one listing each -- and it is the
single-row model that cannot hold the first.

Two decisions follow from that and settle the cost. Every priced thing has a
listing, cash and FX pairs degenerately, and a security whose listing is unknown
carries an empty one, per-security and completed in place as adr/0067 completes
an instrument. Identifiers are security-grain or listing-grain and are never
queried polymorphically: two tables, each with a real foreign key and an
overlap constraint at its own grain. Nothing gives up a capability it would use,
because `identifier.Grain` already declares which level every type names.

The cost, measured rather than guessed: 14 tables hold a foreign key to
`instruments`, 7 of them in a primary key including the `eod_prices`
hypertable; ~7,000 lines of database Go and 102 SQL literals; 3 of the 5 plugin
interfaces and their 30 cassettes; 24 of 52 RPCs and `InstrumentRef` with them;
12 of 16 spec files and around 6 ADRs. The client is nearly untouched. The
valuation query is close to neutral -- almost everything it keys on
`instrument_id` is already a listing fact, and the two places that read both
grains stay at one join given a view and given that every priced thing has a
listing. P01 is the deadline for the cheap version, since proto and schema
changes are free pre-release.

## Open questions

### Knowing the security but not the line

The same question asked in four places, and the one the ADR most needs to answer
once rather than four times. A default listing and an empty listing are also not
the same object -- one says there is exactly one line, the other says how many is
unknown -- and a representation that conflates them cannot be asked which it
holds.

A posting carries `txs.instrument_id`, so it must name a listing, or name the
security when the broker did not say which line: `trading_currency` usually
discriminates and does not always. Holdings then aggregate listing-known and
listing-unknown postings for one security and have to decide whether they net,
since a buy on the GBP line and a sell on the security is either a closed
position or two open ones. `weight_commodity` is `inst:<uuid>` and a group
balances on an exact sum per commodity, so two legs stated at two grains are two
commodities and the group grows a spurious residual -- decided at ingest, before
the line is known, and only a merge rewrites it afterwards. `transfer_matches`,
unique on `(from_group_id, instrument_id)`, never pairs two sides stated at
different grains, and reports them as unmatched transfers indefinitely. And the
identifier lookup gains an outcome it has no home for: an ISIN resolving to one
security while a MIC_TICKER resolves to a listing of another is neither a match
nor adr/0004's merge.

An identifier that points at both grains answers this for identifiers. The
balance check is what fails first.

### Naming a listing

`InstrumentRef` names one instrument by a single identifier and is the only way
an archive refers to one, chosen by `bestIdentifierJoin`, whose priority order
ranks MIC_TICKER above ISIN and so already mixes the grains. A price group is
listing-grain under adr/0035, and a listing holding only security-grain
identifiers -- an empty one above all -- has nothing to be named by. Either the
ref becomes a security identifier plus a venue and a currency, or a listing gets
an invented identifier in the sense of adr/0059. Until that is answered, prices
do not round trip.

The grain of `BROKER_DESCRIPTION` and of a broker's contract identifier (0123)
belongs here, because the `(source, description)` mapping is what makes
resolution cheap. Listing-grain gives one row per line for free under the
existing exclusion constraint, but only where the broker's text distinguishes
the lines; where it does not, the mapping cannot be stored at all. adr/0067's
completion in place then needs a second form at listing grain, with its own test
for when a listing is complete.

### Merge, and the inverse it does not have

A security merge has to merge listing sets, and two survivors on one venue in
one currency collide -- including two empty listings. The harder half is that a
security's empty listing turning out to be two real ones is a split, and nothing
in the system splits anything: transactions, prices, coverage, dividends and
fetch blocks would all have to be reassigned. The prices are the sharp edge,
since they were fetched under whichever line the plugin picked and their
currency is unknown, so they are probably discarded rather than divided.

### Which grain each fact keys on

`cash_dividends` is keyed `(instrument_id, ex_date)` and carries its own
currency, so a security-grain dividend collides on the key the first time one
ex-date pays in two currencies -- the seam is already visible in the schema.
Splits are security-grain and prices listing-grain, yet `split_factor_at`,
`RecomputeSplitAdjustments` and `ApplyOptionSplit` reach both through one
`instrument_id`. An option is a further case: OCC is security-grain because a
contract is cleared in one place, but its deliverable is shares of one line and
its own quote carries a venue and a currency, so whether `underlying_id` names a
security or a listing decides whether the two may disagree about currency.

Price plugins state eligibility at both grains at once: `AcceptableExchanges`
and `AcceptableCurrencies` are listing facts while `SupportedIdentifierTypes`
may be security-grain. So the fetch unit is the listing and the key need not be,
and `price_fetch_blocks`, keyed `(instrument_id, plugin_id)`, must choose
between losing a line the provider does carry and re-asking for every line of a
security it does not.

### Rules that exist because the level is missing

Several identifier rules compensate for having nowhere to put a listing, and
they change meaning rather than code: the listing-level metadata rule in
docs/spec/identifiers.md, `consistentWith` and `fillBlanks`, composite exchange
codes held as a country, and the candidate stage of adr/0058 -- whose stated job
is completing an identity that did not say where the instrument trades, which is
to say picking a listing. Some get simpler and some become wrong. Porting them
mechanically is the mistake available here.

### Listings over time, and movement between them

A listing wants its own validity interval, which makes a redenomination (GBX to
GBP), a venue migration and a delisting representable as themselves rather than
as a new instrument and a merge. The case this issue is about also produces an
event nothing handles: a broker converting a holding from one currency line of a
security to the other, a real quantity movement between two listings with no
economic event behind it.

### The grain the user sees

`portfolio_filters` stores an instrument UUID, `holding_declarations` is unique
on `(user_id, broker, account, instrument_id, as_of_date)`, lots key on the
acquiring posting, and `instruments.name` is denormalized from identifier
priority by trigger. A user filtering on a company means the security; a user
reconciling a statement means the line. Each of these picks one, and the UI has
to show which it picked.

This wants an ADR before any code.
