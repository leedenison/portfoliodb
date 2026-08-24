# Transaction Groups and Postings

A **tx group** is one economic event. The `txs` rows that reference it are its
**postings**. See adr/0022-typed-per-account-cash-flow-boundary.md.

## Postings

A posting is a signed amount of one commodity in one account at one point in time:

| Concept   | Column                        |
| --------- | ----------------------------- |
| Account   | `broker` + `account`          |
| Kind      | `account_type`                |
| Commodity | `instrument_id` + `listing_id` |
| Amount    | `quantity` (signed)           |
| Date      | `order_date` + `trade_date`   |
| Evidence  | `tx_correlations`             |

### Dates

A posting carries two dates, both required.

`order_date` is when the transaction was ordered, and is the date the posting is
filed under: windows, listings, valuation and the grouping rules that bucket on a day
all use this one. `trade_date` is when it took effect -- a trade executed, a charge
was levied, money moved -- and is what `share_count_basis` defaults from, since the
as-traded convention is about when the trade happened.

Two dates rather than one because a broker dates a charge by when it cleared and a
trade by when it settled, and those are days apart: in the Fidelity sample a trade
settles T+2 to T+4 while its dealing fee clears T+0 or T+1. Bucketing on either of
those puts a charge in a different bucket from the trade it belongs to, and the order
date is the one the two agree about. See
adr/0051-a-posting-carries-an-order-date-and-a-trade-date.md.

A source that reports one date writes it to both rather than leaving either unset.
That is a statement that the two coincide for that source, which is true and is
different from saying one is unknown -- an OFX statement has no `DTSETTLE` tag at all,
and a Schwab CSV one date column. So a reader can take either field without first
asking whether this particular broker distinguishes them.

### Correlations

A **correlation** is a broker-neutral statement of why a posting might belong with
another one: an identifier the source issued, what may be compared about it, and over
what set of postings. A posting carries however many its source supplies, in
`tx_correlations`. See adr/0048-correlations-declare-their-own-semantics.md.

| Field          | What it says                                                     |
| -------------- | ---------------------------------------------------------------- |
| `label`        | which series this identifier belongs to, and so what it is comparable with |
| `token`        | the identifier as the source wrote it                            |
| `ordinal`      | the number the token carries, where the converter knew how to take one |
| `scope`        | `FILE`, `ACCOUNT` or `BROKER` -- exactly one                     |
| `matches`      | `EXACT`, `ORDINAL`, `ACCOUNT`, `ATTACHES` -- at least one         |
| `ordinal_span` | how far apart two ordinals can be and still be about one event   |

`scope` and `matches` are two halves of one statement and neither is usable without
the other: `BROKER` means this user's data for this broker, and `ACCOUNT` is not
redundant with `FILE`, because an OFX `FITID` is unique within the account while a
Fidelity export spans accounts. `matches` is a set rather than a choice, because a
Fidelity reference number is honestly both equality-comparable and ordinally
comparable. Two of them are asymmetric, and only the bearer carries either. `MATCH_ACCOUNT`
names *another posting's account* rather than a token that posting carries, which is
what a source-supplied counterparty pointer says. `MATCH_ATTACHES` names *another
posting's identifier*, and says the bearer joins whatever group that posting is
placed in -- a reference rather than a shared key, and additive rather than
partitioning, since it says nothing about the posting it names. That is what a
source reporting a subordinate leg as a record of its own says: a charge naming its
trade, a withheld tax naming its dividend. See
adr/0052-an-attaching-correlation-is-additive.md.

`FILE` scope is stored as the ingestion job that supplied the correlation, since a
file has no identity of its own once its postings are rows. An archive import is one
job, so file-scoped evidence re-imported from an archive is comparable across
everything that archive carried rather than within the uploads it was assembled from.

A converter may only **transcribe**, never infer. A token may be synthesised from the
source's own structure -- nesting, containment, an explicit order column -- and never
from amounts, dates or proximity. Nothing distinguishes a transcribed token from an
inferred one once stored, so this is the discipline the format rests on. A token that
is present always names something the source itself issued.

Every posting a converter reads out of one record carries that record's correlations,
including the legs it derives from a record's other fields -- the commission netted
into a trade's total, the income a reinvestment consumed. Saying that these postings
came out of one record is a fact about the transcription, and it is what the server
puts the record back together from. What a converter may not say is that two separate
records are one event. A leg the server derives -- a boundary leg or a routed residual
-- correlates with nothing, because it transcribes nothing.

A token is not a natural key and carries no uniqueness constraint. Ingestion is
idempotent by replacement (adr/0002-transaction-ingestion-model.md) and one source
transaction can produce several postings sharing a reference.

A `MATCH_ACCOUNT` token is advisory rather than authoritative. A source can use the
field it comes from for something else -- Fidelity puts the product account a service
fee was charged for in the same field it names a transfer's source in, which is
attribution rather than a transfer counterparty -- so it is read as a pointer only for
a group that produced a `TRANSFER_CLEARING` residual.

Correlations are what the grouping pass reads to decide the partition, described in
[Where grouping is decided](#where-grouping-is-decided). They are stored rather than
consumed at ingest because that pass runs over stored data, and because a rebuild from
an archive would otherwise have nothing left to group on. [Transfer
matching](#transfers) reads them too, which is a different question -- which two groups
are the two halves of one movement, not which postings are legs of one event.

Currencies are instruments, so a cash movement is an ordinary posting and needs no
separate representation. Nothing in the read path distinguishes a cash posting from a
security posting: holdings, valuation, price coverage and holding declarations all
aggregate `SUM(quantity)` grouped by security and line, and a cash balance is that
sum over a currency instrument.

## Account types

Double-entry needs somewhere to post the other side of events that are one-sided in the
source data: an INITIALIZE pad has no counterparty, most brokers report a dividend or a
charge as a single cash row, and the two sides of a transfer arrive in separate
statements. `account_type` classifies the account a posting lands in. The posting keeps
the `broker` and `account` of the event it belongs to, so a residual stays attributable
to the account that produced it.

| Value               | Meaning                                                   |
| ------------------- | --------------------------------------------------------- |
| `USER`              | An ordinary broker account posting. The default.           |
| `EQUITY`            | Value entering or leaving the user's holdings entirely.    |
| `INCOME`            | Dividends, interest and other income.                      |
| `EXPENSE`           | Commissions, levies, custody and service charges, taxes.   |
| `IMBALANCE`         | The residual of a group that does not sum to zero.         |
| `TRANSFER_CLEARING` | One side of a transfer whose other side is not yet known.  |
| `SOURCE_ROUNDING`   | A residual small enough to be the source rounding its own figures. |

Opening balances are `EQUITY` postings rather than a type of their own. An opening
balance is one use of equity, and a withdrawal to a bank is an equity posting too; an
enum that mixes roots with specific accounts ages badly. An INITIALIZE pad is written
with exactly such a counterparty -- equal and opposite, same instrument, same account,
same group -- which is what lets its group sum to zero. See
[fixed-point.md](fixed-point.md#the-equity-counterparty).

The type is a column rather than a reserved prefix on `account`, because `account` is
user-supplied free text that a broker CSV can collide with, a name has nowhere to record
which broker account a residual came from, and the currency is already the posting's
commodity. See adr/0022-typed-per-account-cash-flow-boundary.md.

Constraining an account to be unique per type would need an accounts table, and accounts
today are implicit: distinct `(broker, account)` pairs in `txs`. Until then
`(broker, account, account_type)` is the account identity by convention rather than by
constraint.

## The cash-flow boundary

Classification is per account and fixed at ingest. Netting is per portfolio and resolved
at query time. A posting in account A is an external flow of A iff its group has a leg
outside A, and `account_type` says what kind of outside:

- `EQUITY` is external, and never nets, since there is no counterparty side.
- `INCOME`, `EXPENSE`, `IMBALANCE` and `SOURCE_ROUNDING` are not flows. These are return
  and cost, and treating them as external would strip dividends out of the return and
  report it gross of fees. `IMBALANCE` is internal because a residual is usually a
  missing fee or a missing cash leg, both of which are internal. `SOURCE_ROUNDING` is
  internal more plainly still: a source disagreeing with its own arithmetic is neither a
  contribution nor a withdrawal.
- Another `USER` account is external to A, and nets against the other side when both
  accounts belong to the portfolio being measured.
- `TRANSFER_CLEARING` is external while unmatched, because one half is all we know. It
  nets once the pair is [matched](#transfers) and both accounts are members.

Membership decides internal versus external; there is no per-portfolio user override.
Membership already expresses the intent, and a toggle would be a second place to say the
same thing that can disagree with the first.

Two things the list leaves implicit. A group contributes a flow only where it has a leg
inside the portfolio: without that, a dividend in an account the portfolio does not hold
has exactly one leg that is outside and external -- its own -- and reads as a
withdrawal from a portfolio it never touched. And a flow is dated by the external leg
rather than by the member leg whose value moved, because the external leg is the
counterparty and its date is the one the source stated for the crossing; a group's
postings need not share a date, and choosing among several member legs would be
arbitrary.

Membership is per posting rather than per account, which an instrument filter makes
visible: a buy's cash leg does not match a portfolio scoped to one security, so the
cash reads as a flow into it. That is the right answer for such a portfolio -- the cash
bought in -- and it is the same answer valuation gives, which does not value that cash
either.

The flows crossing the boundary are read per portfolio and per user, in the commodity
they moved in. A flow in shares -- an in-specie transfer, or the `EQUITY` counterparty
of a pad -- would need a price as well as an exchange rate to state as money, and the
result would be an estimate where a contribution figure is exact
(see [0026](../adr/0026-exact-decimals-bounded-by-closure.md)). Converting is the
concern of whatever computes a return from them.

## Visibility

Non-`USER` postings are excluded from holdings and from every quantity aggregation, so
that no residual or in-flight position appears among a user's holdings and so that a
counterparty leg cannot net against the holding it balances. The predicate is
`account_type = 'USER'` and it is applied per call site rather than in
`portfolio_matched_txs`, because valuation does not stay the same as holdings.

Valuation is a different question. When a portfolio contains both sides of a matched
transfer and the two sides are dated by their own statements, excluding
`TRANSFER_CLEARING` makes the transferred holding vanish for the days in between -- a dip
in portfolio value and a fake return blip. Holding value in transit is what a clearing
account is for. So: exclude from holdings display always; include in valuation only for
matched pairs where both accounts are members. Including an unmatched in-flight balance
would assert the money is coming back to a member account, which is the thing we do not
know.

Valuation reads the pairing through `portfolio_in_flight_txs`, which names the clearing
legs a portfolio values. Membership is tested against the counterpart's own clearing
leg rather than against its group, because that leg carries the counterpart account and
the commodity the match is keyed on, so the test reads the same from either side and a
pair is admitted whole or not at all. The counterpart is not required to fall inside the
valuation window: value still in transit when the window closes is value held, and
asking for its arrival would reinstate the dip the rule exists to remove. Valuing a
user rather than a portfolio needs no membership test at all, both groups being the
same user's by construction.

Two things follow. Valuation and holdings disagree while a transfer is in transit, by
design: the value is in the total and the position is in neither account's holdings.
And a valuation of a past window moves when the matcher runs, because what nets is read
at query time rather than stored -- the price of not recording a decision that depends
on data which has not arrived.

The transaction list is not filtered. It is a ledger view, and hiding the counterparty
legs would make groups look unbalanced and hide the residuals that make a converter's
lossiness measurable.

## Groups

| Column      | Notes                                                              |
| ----------- | ------------------------------------------------------------------ |
| `id`        | uuid, PK                                                            |
| `user_id`   | uuid, FK -> users                                                   |
| `timestamp` | The date of the event: the earliest `order_date` of its postings.    |
| `job_id`    | The ingestion job that created the group. NULL when system-derived. |
| `created_at`| timestamptz                                                         |

`job_id` is **not** a foreign key. A group must outlive its job, and if job rows are
ever pruned by age the id still distinguishes one creation from another and still
groups everything written by the same upload.

Every posting belongs to exactly one group, and `txs.group_id` is `NOT NULL` so that
none can be written without one. A lone posting is a group of one. The balance
invariant is stated per group, so a posting outside every group would be a row it
could not reach.

## Balancing

Every group is balanced when it is stored. Whatever its postings leave over is routed
to an explicit counterparty rather than rejected, so the invariant holds by
construction from day one and a residual becomes measurable instead of being absorbed
into a cash balance.

**The store does the balancing, from the stored weights.** A converter emits the
postings its source stated; the ingest pipeline weighs them; the store writes the
postings and then settles each group it touched, in the same transaction. An upload,
a period replace and a regroup all reach the same code, so none of them can disagree
about what a group owes, and no caller can leave one unbalanced.

Two kinds of leg are written for it, in that order.

A **boundary** leg is the other side of a posting whose own type names where its money
came from or went to: `INCOME` for a dividend, `EXPENSE` for a charge. The test is
must-be over the resolved type, so a row that is income under every reading gets an
income leg and one whose declared set left the question open gets none -- inventing a
leg for one reading would assert it. It is written per posting rather than per group,
because a dividend and a charge in one group must produce a leg each: netting them
would post the difference to whichever account won.

A **residual** is what is left after that: `IMBALANCE` for a leg the source omitted,
`TRANSFER_CLEARING` for the unmatched side of a journal, `SOURCE_ROUNDING` for a
difference small enough to be the source disagreeing with itself.

Both are derived from the postings around them and neither is an input. They are
recorded as such in `synthetic_purpose` -- `BOUNDARY` and `RESIDUAL` against the NULL a
posting a source stated carries -- and they are deleted and written again whenever a
group's membership changes, whether by a replace or by a regroup. The account type
cannot record this, because a leg a converter read out of a record lands in the same
account types.

Each posting **stores** what it contributes to that balance, in two columns:

| Column             | Notes                                                              |
| ------------------ | ------------------------------------------------------------------ |
| `weight`           | The amount contributed. `NUMERIC`, exact.                           |
| `weight_commodity` | What it is contributed in: `cur:<code>`, `inst:<uuid>` or `desc:<text>`. Never empty. |

A posting names a security always, and the currency line within it when something
said which -- the stated trading currency, the line identification resolved, or
the security having exactly one. Where nothing did, `listing_id` is null: the
position is real and its quantity is right, but which currency it is denominated
in is unknown.

A holding declaration says the same pair, for the same reason. It is a statement
about a holding, a holding is per line, and two lines of one security are two
holdings an FX rate apart -- so one security may carry a declaration on each of
its lines at one date, each checked against that line's postings and each padded
by its own INITIALIZE posting. A declaration that names no line is the statement
about the holding nothing could place, and it is unpriced exactly as the postings
under it are. Where a request states no line, the server settles it the way ingest
settles a posting's last rung: the security's sole line, and no line where it has
several.

What it *balances* in is the security, which is why `weight_commodity` names one.
A group's legs have to be weighed at one grain or a residual appears that nothing
put there, and the line is not available for every posting. So two lines of one
security are one commodity for balance and two holdings for valuation: the same
security, an FX rate apart. See
adr/0072-a-posting-names-a-security-and-a-line.md.

A group balances when `SUM(weight)` is zero for each `weight_commodity`, and a
`DEFERRABLE INITIALLY DEFERRED` constraint trigger enforces exactly that. Enforcing it
in the database rather than the application makes it unbypassable: no code path, no bad
import and no manual psql session can leave an unbalanced group behind.

The check is deferred to COMMIT because the legs of a group can be written in any
order, and each leg on its own leaves the group unbalanced. A group deleted whole
matches no rows and passes vacuously, which is what lets replace-by-period delete
groups and let the cascade take their postings; deleting a single leg does not.

It is also what lets a replace delete part of a group. Deleting one leg of a balanced
group is a violation on its own; the counterparty that negates what is left is written
in the same transaction, so the group is settled before COMMIT.

The exactness is what makes the check arguable-free. There is no tolerance in it,
because every non-zero residual is routed when the group is stored -- including the
sub-tolerance ones, which go to `SOURCE_ROUNDING` -- so a group sums to zero by
construction. It reads the
raw `quantity` and `unit_price`, never the split-adjusted pair, which carries a rounding
an exact check would reject.

It costs about 5% of an import: 21us per posting at COMMIT against 413us to insert one
(see `server/db/postgres/balance_bench_test.go`).

The weight is stored rather than re-derived on read because instrument state moves
under a posting after ingest: a merge rewrites `instrument_id` and `listing_id`
wholesale and `contract_multiplier` records what a corporate action left behind,
so a re-derived weight could disagree with the one the group was balanced on. The cost is that the
constraint proves the *declared* weights of a group sum to zero rather than that its
postings balance; see adr/0029-posting-weight-is-stored.md for why that is the right
trade. Nothing maintains the columns after ingest except the merge, which rewrites
`weight_commodity` alongside `instrument_id` in the same statement, and the
fill-in below, which writes the line alone. The line is never the commodity, so
neither disturbs a weight.

**A line, once it exists, claims the postings that stated it.** A posting naming a
currency its security has no line for names no line at ingest, because no rung
mints one. When something does name that line -- a provider, a listing-grain
identifier, a merge -- the postings of that security on no line whose
`trading_currency` is in its family acquire it. That is a fill-in rather than a
move: nothing has to be taken off a row first, and the holding stops reporting
unpriced the moment something names the line it was always on. A pad already on
the line wins over one on none, the INITIALIZE key being per holding. See
adr/0071-listings-merge-by-currency-and-an-unknown-one-splits.md.

A group's postings are in different commodities, so a plain `SUM(quantity)` cannot
say whether it balances: a buy is `+10 AAPL` and `-1855 USD`. Balance is checked on
**weight**. A posting converts to the settlement currency at its `unit_price` when
the units its counter-leg is expected in differ from its own; otherwise it weighs
its own quantity in its own commodity. The declared type says which, under the
every-candidate rule of [tx-types.md](tx-types.md):

| broker_tx_type                       | Other side expected in | Converts               |
| ------------------------------------ | ---------------------- | ---------------------- |
| must be `TRADE_ASSET`                | money                  | yes                    |
| money posting, any other type        | the same currency      | only across currencies |
| security posting, any other type     | the same commodity     | no                     |

An ambiguous set does not convert -- a rule fires only if it holds for every
candidate -- and weight neutrality makes that harmless: a priced set whose
members would weigh differently is rejected at ingest, so declared ambiguity
never moves a weight (see
adr/0046-declared-ambiguity-is-bounded-by-weight-neutrality.md).

"Across currencies" means `trading_currency != settlement_currency` -- a EUR
dividend settling into a USD account, where `unit_price` is the FX rate. Two guards
complete it: a leg already denominated in the settlement currency never converts,
being already in the units the group balances in; and a posting with no price
cannot convert at all, so an exchange event whose source omitted a price leaves its
residual in the security itself. See
adr/0024-group-balance-is-checked-on-weight.md.

A price is per underlying unit, so converting also multiplies by the instrument's
**contract size**: `100 * contract_multiplier` for an option, and 1 for anything
quoted in the units it trades in. The 100 is the OCC standard deliverable and
belongs to the asset class, while `contract_multiplier` records only the deviation
a corporate action can leave behind, so a standard contract needs nothing stored.
A future's size varies per contract and is not held anywhere, so a future weighs as
though its size were 1 (0072).

Weight is computed from the raw `quantity` and `unit_price`, never from the
split-adjusted pair, which carries a rounding an exact check would reject (see
adr/0028-cumulative-split-factor-is-an-exact-rational.md).

Weights accumulate **per commodity**, so a group can produce more than one routed
posting and the commodity is whatever is left over -- cash for a missing cash leg,
the security for an unpaired security transfer.

Weights are exact: a posting's weight is `quantity * unit_price * contract_size`,
which is closed under multiplication, so a group's balance is a plain sum with
nothing to absorb. Every non-zero residual is routed, and only a group that sums to
exactly zero produces no posting at all.

The routed posting takes the `IMBALANCE` type, or `TRANSFER_CLEARING` when any
of the group's legs resolved under `TRANSFER`; a leg whose declared set nothing
has narrowed resolves `AMBIGUOUS` and routes to `IMBALANCE`, because it is not a
transfer under every reading. The posting keeps the broker, account, date,
declared set and resolved type of the group it balances, so the residual stays
attributable to the account and the kind of event that produced it. Its commodity is
carried by `instrument_id`, never encoded in a name, and it takes the line every leg
it balances shares -- none where they differ, the sum being per security-grain
commodity and so silent about which line the leftover is on. It is written into the group it balances, and a replace that cuts that group
deletes it along with the in-period postings: the remainder is routed fresh, so a group
carries one residual per commodity however many replaces have reached it.

### Source rounding

A residual below a **tolerance** takes the `SOURCE_ROUNDING` type instead. The
tolerance is not a floating-point fudge and did not go away when quantities became
exact: a broker quoting a price to one precision and a cash amount to another
disagrees with itself, and that difference is exact and real but is an artefact of how
the statement was written rather than a leg the converter missed.

There are two roundings, because there are two figures:

```
tolerance = half a cent                                         (the cash figure's own rounding)
          + SUM over converted legs of |units| x half the last digit of the price
```

A cash amount written to 2dp is out by up to half a cent however large it is. A price
written to 2dp is out by up to half a penny *per unit*, so a holding of 2,676 units is
out by 10.30 -- and a group balances on weight, where that lands. A fixed half-cent
tolerance covers only the first, and so reports every large trade as an imbalance.

A leg contributes only where its weight was derived by pricing it into the settlement
currency. A cash leg does not: its price of `1` is exact by definition rather than a
rounded quote, which is what leaves a deposit run's tolerance where it was. A residual
denominated in a security keeps `1e-6`, since an unpriced leg was never converted.

The price's precision is **assumed, not read**. Fidelity strips trailing zeros in its
own download -- the sample export writes prices at 0, 1 and 2 decimal places, with
`47.1` and `47.11` in one instrument's series -- so a stated scale understates what was
quoted, and reading it off the figure inflates the bound tenfold on a 1dp price. The
floor is 2 decimal places (`residual.PriceScaleFloor`) and it is a floor rather than a
fixed precision, so a source quoting finer keeps its own: the sample IBKR statements
run to 6dp and Schwab to 4dp.

The sum is a worst case, assuming every leg's price erred the same way, so a small
missing leg can hide inside the bound of a high-quantity trade. That is the cost of
not reporting the source's own arithmetic as missing data. The bound is never below
half a cent, so nothing that was rounding becomes an imbalance. See
adr/0026-exact-decimals-bounded-by-closure.md.

The tolerance decides the **type** of the routed posting, not whether one is written.
Suppressing the small residuals would leave the group summing to a small non-zero
value, which is what the balance constraint rejects; folding them into `IMBALANCE`
alongside genuinely missing legs would throw away the one thing already known about
them. A sub-tolerance residual on a journal is rounding too, so `SOURCE_ROUNDING`
beats `TRANSFER_CLEARING` rather than the other way round.

Rounding balances appear in the imbalance report under their own tab -- one posting
is noise, but the per-broker total and posting count are the only place the cost of a
source's rounding is visible -- and are deliberately absent from the dashboard
counts, which ask whether something is wrong.

**The tolerance applies only to a group holding every leg its source stated.** It is a
claim about where a difference came from, not about how big it is: two figures the
source rounded differently. A group something has been taken out of -- cut by a
replace, or left by a posting the engine moved elsewhere -- is short by the value of
what went, which can be small by coincidence, so the same size means something else
entirely and the residual takes its family type whatever its size. Filing it as
rounding would hide a fragment from both the dashboard and the grouping cycle, which
seeds from `IMBALANCE` and `TRANSFER_CLEARING` and passes over rounding. Which of the
two a group is in is known only to whatever just changed it.

An INITIALIZE pad is balanced by an `EQUITY` counterparty instead; see
[fixed-point.md](fixed-point.md#the-equity-counterparty).

### Transfers

The two sides of a journal (a leg resolved under `TRANSFER`) are not paired at
ingest. Brokers report them in separate statements and sometimes in separate imports,
so each side is balanced by a `TRANSFER_CLEARING` counterparty in the same commodity,
which holds the value in transit.

They are paired afterwards, by a background job. Two things run it, on one buffered
channel: an admin RPC, which is what an external cron job or CLI calls and is how
matching runs on a cadence, and the ingestion worker once an import commits, so a
transfer whose second side has just landed does not wait for the next tick. A cycle
reads every side no match names and writes nothing when there is nothing new, so
running it often is cheap.

A match is a link between the two tx groups -- both group ids, the commodity, the
currency line each side sat on, how they were matched and when -- rather than a status
on the posting or a third group closing both sides out.
It records which group, and so which account, holds the other side, because that is
what the membership test above consumes. The link is derived and disposable: it
cascades when a re-upload replaces one side's groups, and the job rebuilds it. See
adr/0037-transfer-matches-are-links-not-postings.md.

The commodity is the security and the two lines are recorded beside it rather than
keyed on. A residual carries a line only where every leg it balances shares one, so
the security is the only grain every side has; and the two lines differ exactly when
the transfer is a broker converting a holding between two currency lines of one
security, which pairing at line grain would make unrepresentable. Either line is
absent where its side named none.

A pair is found on evidence that identifies the occurrence, in this order:

1. **An explicit pointer** -- the source names the other account outright, in a
   correlation declaring `MATCH_ACCOUNT`.
2. **Reference proximity** -- the two sides carry correlations declaring
   `MATCH_ORDINAL` whose ordinals are near, a broker issuing the references of one
   movement together.

Each pass reads the correlations of the whole group rather than of the clearing leg,
which is routed and transcribes nothing. Which pass can read a given correlation is
the correlation's own to say: an OFX `FITID` declares neither `MATCH_ORDINAL` nor
`MATCH_ACCOUNT`, so both passes pass over it, which is right -- it is opaque and
unique within one account. Whether an identifier carries a number is the converter's
to say, since it is the only thing that knows its broker's numbering.

Both additionally require an exactly equal and opposite amount in the same commodity,
two different accounts of the same user, and a date window. The window exists because
the two sides are dated by their own statements and need not agree, and is bounded
above by the staleness threshold of the report below, so that a pair still within the
matcher's reach is never reported as missing. A pointer is narrowing evidence rather
than a decision: it names the counterparty account, not the occurrence, so it needs
the window and the ambiguity test like anything else.

**Ambiguity is left unmatched.** Where two candidates are equally good on the
evidence, neither is taken. There is no rule that pairs on an amount and a date alone,
so a transfer between two brokers -- the case with no sample to calibrate against --
stays unmatched and visible.

A residual `TRANSFER_CLEARING` balance therefore means a side whose pair has not
arrived, and its age is the age of something missing.

## What an upload carries

An upload carries postings and says nothing about which of them are legs of one
event. Nothing on the wire can: the partition is
[derived](#where-grouping-is-decided) from the evidence the postings carry, and a
field stating one would be a second thing deciding it -- one the server could
neither justify nor contradict.

Each posting is stored alone and then partitioned, in the transaction that wrote it.
A group is a thing the server concluded, never a thing it was told.

Group ids are not durable. Re-uploading a period produces new ones, and so does a
cycle that repartitions the region. This follows from transactions having no natural
key (see adr/0002-transaction-ingestion-model.md): there is nothing stable to key a
durable group identity on, which is why a hand-made assertion is a correlation on
the postings it names rather than a reference to a group
(adr/0049-a-human-assertion-is-a-correlation.md).

A manually added posting is treated the same way. It is stored, partitioned and
balanced like any other, so an appended trade that accounts for nothing gets a routed
counterparty rather than a group the balance invariant cannot reach.

## Deletion

The group is the unit of deletion. Deleting a group deletes its postings, so no code
path can leave half an economic event behind by deleting the group it belonged to.

Bulk upload replaces a period (see adr/0002-transaction-ingestion-model.md) by
deleting **the postings** inside it, not the groups that hold them. A group's postings
need not share an order date -- the Fidelity deposit-run pass groups on reference
proximity rather than the date bucket, because a run in the sample export settles
across two days -- so a group can straddle any boundary, and deleting it whole would
take legs the upload that triggered the delete does not carry and nothing would
re-insert. Widening the period until it holds whole groups is not available either,
because the upload does not cover the widened range; see
adr/0040-delete-window-widens-only-to-dataset-coverage.md.

A group left with nothing is deleted with its postings, which is every group that did
not straddle. A group left with something keeps it exactly where it is, is re-dated to
its earliest surviving posting, and gets a counterparty routed for what those postings
no longer balance to. Its own routed counterparties are re-derived rather than kept, so
a group carries one residual per commodity however many replaces have reached it. See
adr/0039-replace-by-period-deletes-postings-not-groups.md.

A posting survives a replace unless it is a routed counterparty, or a non-synthetic
posting of that upload's own broker inside the period. So a leg of another broker
survives -- an upload speaks only for its own -- and so does a synthetic INITIALIZE
posting, which is the declaration machinery's rather than ingestion's (see
fixed-point.md).

The routed counterparty is classified by family, so a journal keeps
`TRANSFER_CLEARING`, but never as source rounding: a residual left by a cut is the
value of the legs removed, not the source disagreeing with itself, so it is not
`SOURCE_ROUNDING` however small it is.

The result is stable under repetition rather than identical to what came before:
re-importing a period leaves the out-of-period legs where they were and the in-period
legs in a new group, each balanced by a routed residual. Two groups where a converter
wrote one; rejoining them is the grouping pass's job (see
adr/0041-server-owns-transaction-grouping.md).

## Where grouping is decided

The server decides which postings are legs of one event, from evidence stored on the
postings themselves. A converter transcribes what its source wrote and states what
that source correlated; it does not hand down a pairing for the server to obey. This
is what lets legs that arrived in separate uploads be joined at all, and what lets a
group a period replace cut be put back together. See
adr/0041-server-owns-transaction-grouping.md, and
adr/0021-converters-own-transaction-grouping.md for the arrangement it replaced.

The evidence is the postings' correlations, their declared type sets, and their
amounts -- quantity, unit price, and the cash total the source stated for a row whose
own quantity is not money. Nothing else, so the same stored data partitions the same
way whenever it is asked.

**Rules claim in a fixed precedence order.** Each rule is one way of deciding that
postings belong together, and precedence is a number the rule carries rather than the
order of a call site, so an ordering that differs per broker is a table rather than a
restructuring. A claim is irrevocable: a later rule may neither add to it nor take a
posting out of it. Within a rule, candidates are ranked across the whole region before
any is taken, so one claim cannot strand another. See
adr/0047-grouping-runs-as-precedence-ordered-passes.md.

**The rule that claims a posting is what resolves it.** A rule may claim a posting
only where its declared set admits that rule's type, and claiming settles
`resolved_tx_type` there and then. There is no narrowing phase afterwards, which is
what dissolves the circularity of grouping consuming the type as evidence while the
type depends on the group. See adr/0044-tx-type-is-declared-and-resolved.md and
[tx-types.md](tx-types.md).

Attaching claims last, and has to. A pointer says nothing about the posting it names,
so it cannot decide where that posting goes -- only follow it. Running it earlier
would let a charge claim a trade's asset leg before the trade rules could pair it with
its cash leg, and strand the cash row. Its rule is the only one that adds to a group
another rule assembled, through the one engine operation that names an anchor and the
postings contributed to it.

Exact token equality claims first. A source that states its own grouping states it as
a shared identifier -- OFX stamps one FITID on every leg of the record it describes --
and a person asserting a grouping does the same with a token nobody transcribed
(adr/0049-a-human-assertion-is-a-correlation.md). Re-deriving either by inference
would replace a stated fact with a guess. How far a token reaches, and what may be
compared about it, are the correlation's own declaration rather than the rule's
assumption; see adr/0048-correlations-declare-their-own-semantics.md.

**A neighbourhood is recomputed, not repaired.** A cycle starts from seed postings,
grows the region the rules would read until it stops growing, and partitions all of it
from scratch. Widening is free, because it reads stored data and fetches nothing, and
it is bounded because a rule may state no reach that is not an indexed query. See
adr/0050-grouping-recomputes-a-neighbourhood.md.

Only the disagreements are written. A derived group whose membership is exactly a
stored group's produces no statement at all: it keeps its id, and so do the transfer
matches keyed on that id. That is what lets a cycle run over a region far wider than
any upload without churning ids for postings nobody touched.

A regroup deletes the legs the server routed for every group it touches -- the
residuals and the boundary legs alike -- and writes fresh ones in the same transaction
as the membership change. Neither carries evidence, so neither can be repartitioned: a
residual is arithmetic on the legs of its group, and a boundary leg mirrors one leg's
weight, so once those move both are arithmetic on nothing. Every intermediate state is
unbalanced, which is what the deferred balance constraint in
[Balancing](#balancing) makes expressible; leaving the routing to a later statement
would expose a moment where the constraint fires on data that was valid before the
regroup began.

A group the engine assembled is re-routed as a whole group and one a posting left as a
shortened one, which is the distinction [Source rounding](#source-rounding) turns on.
The engine pairs money figures that differ by up to half a cent, which is the fixed
half of the tolerance the balancer rounds at and not the whole of it. The two are
different questions and stay separate: the engine compares two figures the **source
stated** -- a trade's own total against its cash row -- where no price arithmetic has
happened and half a cent is the whole of the error, while the balancer compares a
**derived weight** against a stated figure and so also carries the price's rounding.
A trade whose stated total matches its cash row exactly can still be 10.30 out by
weight. Reading either as a missing leg would report correctly paired trades as
imbalances and seed the next cycle from them.

It runs on an admin RPC, which is how an external cron job gives it a cadence, and
again when a transaction import commits, so legs that have just landed beside older
ones are joined without waiting for the next tick. A job rather than part of ingestion
for the reason transfer matching is one: the partition is a function of all stored
state rather than of one upload's payload.

An upload does not wait for a cycle. The store partitions the postings it writes in
the transaction that writes them, seeded from those postings and grown over the
region the rules reach -- so legs that land beside ones already stored are joined
there and then, and no group is ever observed in whatever shape the postings happened
to arrive in. What the cycle is for is what an upload cannot see: fragments left by a
period replace, and legs whose counterparts arrive later.

Routing a residual is not the server inventing a leg. A residual is arithmetic on the
legs supplied -- what they leave over -- and it is typed as a residual rather than
posted as the cash or the fee the server cannot know it to be. A derived cash leg
would be an invention, and would double count against the cash row a broker already
reports. A group that arrives with its cash row weighs to zero and has nothing routed
to it.

A boundary leg is not an invention either, and for a different reason: it is not
guessed from the arithmetic but named by the posting's own declared type, under the
must-be test that refuses to name one where the source left the question open. See
[Balancing](#balancing).
