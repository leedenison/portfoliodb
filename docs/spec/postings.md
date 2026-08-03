# Transaction Groups and Postings

A **tx group** is one economic event. The `txs` rows that reference it are its
**postings**. See adr/0022-typed-per-account-cash-flow-boundary.md.

## Postings

A posting is a signed amount of one commodity in one account at one point in time:

| Concept   | Column                        |
| --------- | ----------------------------- |
| Account   | `broker` + `account`          |
| Kind      | `account_type`                |
| Commodity | `instrument_id`               |
| Amount    | `quantity` (signed)           |
| Date      | `timestamp`                   |

Currencies are instruments, so a cash movement is an ordinary posting and needs no
separate representation. Nothing in the read path distinguishes a cash posting from a
security posting: holdings, valuation, price coverage and holding declarations all
aggregate `SUM(quantity)` grouped by instrument, and a cash balance is that sum over
a currency instrument.

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

Opening balances are `EQUITY` postings rather than a type of their own. An opening
balance is one use of equity, and a withdrawal to a bank is an equity posting too; an
enum that mixes roots with specific accounts ages badly.

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
- `INCOME`, `EXPENSE` and `IMBALANCE` are not flows. These are return and cost, and
  treating them as external would strip dividends out of the return and report it gross
  of fees. `IMBALANCE` is internal because a residual is usually a missing fee or a
  missing cash leg, both of which are internal.
- Another `USER` account is external to A, and nets against the other side when both
  accounts belong to the portfolio being measured.
- `TRANSFER_CLEARING` is external while unmatched, because one half is all we know. It
  nets once the pair is matched and both accounts are members.

Membership decides internal versus external; there is no per-portfolio user override.
Membership already expresses the intent, and a toggle would be a second place to say the
same thing that can disagree with the first.

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
know. Valuation reads `USER` only until transfer matching supplies the pairing.

The transaction list is not filtered. It is a ledger view, and hiding the counterparty
legs would make groups look unbalanced and hide the residuals that make a converter's
lossiness measurable.

## Groups

| Column      | Notes                                                              |
| ----------- | ------------------------------------------------------------------ |
| `id`        | uuid, PK                                                            |
| `user_id`   | uuid, FK -> users                                                   |
| `timestamp` | The date of the event. The postings carry their own timestamps.     |
| `job_id`    | The ingestion job that created the group. NULL when system-derived. |
| `created_at`| timestamptz                                                         |

`job_id` is **not** a foreign key. A group must outlive its job, and if job rows are
ever pruned by age the id still distinguishes one creation from another and still
groups everything written by the same upload.

Every posting belongs to exactly one group. `txs.group_id` is nullable only so that
raw fixtures can write a posting without one; no production write path leaves it
unset.

## Balancing

Every group is balanced at ingest. Whatever its postings leave over is routed to an
explicit counterparty rather than rejected, so the invariant holds by construction
from day one and a residual becomes measurable instead of being absorbed into a cash
balance. The database constraint that enforces it is not switched on yet.

A group's postings are in different commodities, so a plain `SUM(quantity)` cannot
say whether it balances: a buy is `+10 AAPL` and `-1855 USD`. Balance is checked on
**weight**. A posting converts to the settlement currency at its `unit_price` when
the units its counter-leg is expected in differ from its own; otherwise it weighs
its own quantity in its own commodity. `tx_type` says which:

| tx_type                                                     | Other side expected in | Converts               |
| ----------------------------------------------------------- | ---------------------- | ---------------------- |
| `BUY*`, `SELL*`, `REINVEST`, `CLOSUREOPT`                    | money                  | yes                    |
| `INCOME`, `INVEXPENSE`, `MARGININTEREST`, `RETOFCAP`, `CASHFLOW` | the same currency  | only across currencies |
| `TRANSFER`, `JRNLFUND`                                       | the same commodity, another account | only across currencies |
| `JRNLSEC`                                                    | the same security, another account  | no        |

"Across currencies" means `trading_currency != settlement_currency` -- a EUR
dividend settling into a USD account, where `unit_price` is the FX rate. Two guards
complete it: a leg already denominated in the settlement currency never converts,
being already in the units the group balances in; and a posting with no price
cannot convert at all, so an exchange event whose source omitted a price leaves its
residual in the security itself. See
adr/0024-group-balance-is-checked-on-weight.md.

Weights accumulate **per commodity**, so a group can produce more than one routed
posting and the commodity is whatever is left over -- cash for a missing cash leg,
the security for an unpaired `JRNLSEC`. A residual is routed only above a
tolerance: half a cent for money, `1e-6` otherwise. The constants are interim, but
the tolerance is not -- a group written to 2dp that balances to within half a cent
is balanced.

The routed posting takes the `IMBALANCE` type, or `TRANSFER_CLEARING` when the
group is a journal. It keeps the broker, account, date and `tx_type` of the group
it balances, so the residual stays attributable to the account and the kind of
event that produced it. Its commodity is carried by `instrument_id`, never encoded
in a name. It is written into the group it balances, so replace-by-period takes it
with the cascade.

An INITIALIZE pad is balanced by an `EQUITY` counterparty instead; see
[fixed-point.md](fixed-point.md#the-equity-counterparty).

### Transfers

The two sides of a journal (`TRANSFER`, `JRNLFUND`, `JRNLSEC`) are not paired at
ingest. Brokers report them in separate statements and matching is unreliable, so
each side is balanced by a `TRANSFER_CLEARING` counterparty in the same commodity,
which holds the value in transit. A non-zero `TRANSFER_CLEARING` balance means a
side whose pair has not arrived. Matching them is a later change; until then an
unmatched balance is surfaced for review.

## Naming a group on upload

An uploaded tx carries an optional `group_ref`: an opaque key, scoped to that
upload, naming the event the posting belongs to. Txs sharing a non-empty `group_ref`
are stored in one group; an empty one means the tx is its own single-posting group.
The group takes the timestamp of the first leg that names it.

`group_ref` is not stored and carries no meaning across uploads, so re-uploading a
period produces new groups. This follows from transactions having no natural key
(see adr/0002-transaction-ingestion-model.md): there is nothing stable to key a
durable group identity on.

Single-transaction uploads ignore `group_ref`: the upload is one group whatever it
says. The uploaded tx and the counterparty routed to balance it are stored as that
group, so an appended posting is balanced like any other.

## Deletion

The group is the unit of deletion. Deleting a group deletes its postings, so no code
path can leave half an economic event behind.

Bulk upload replaces a period by deleting the groups whose postings fall inside it
(see adr/0002-transaction-ingestion-model.md). Synthetic INITIALIZE postings are
managed by the declaration machinery rather than by ingestion, so their groups are
excluded from that delete and survive a replace (see fixed-point.md).

## Where grouping is decided

The broker-specific converter decides which postings are legs of one event; the
server persists what it is given and never infers a missing leg, pairs rows, or folds
a fee into a cash amount. Fees are expressed as postings with `type=INVEXPENSE`, not
as a column on the upload. See adr/0021-converters-own-transaction-grouping.md.

Routing a residual is not an exception to that. A residual is arithmetic on the legs
supplied -- what they leave over -- and it is typed as a residual rather than posted
as the cash or the fee the server cannot know it to be. A derived cash leg would be
an invention, and would double count against the cash row a broker already reports.
A group that arrives with its cash row weighs to zero and has nothing routed to it.
