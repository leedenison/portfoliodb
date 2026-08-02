# Transaction Groups and Postings

A **tx group** is one economic event. The `txs` rows that reference it are its
**postings**. See adr/0022-typed-per-account-cash-flow-boundary.md.

## Postings

A posting is a signed amount of one commodity in one account at one point in time:

| Concept   | Column                        |
| --------- | ----------------------------- |
| Account   | `broker` + `account`          |
| Commodity | `instrument_id`               |
| Amount    | `quantity` (signed)           |
| Date      | `timestamp`                   |

Currencies are instruments, so a cash movement is an ordinary posting and needs no
separate representation. Nothing in the read path distinguishes a cash posting from a
security posting: holdings, valuation, price coverage and holding declarations all
aggregate `SUM(quantity)` grouped by instrument, and a cash balance is that sum over
a currency instrument.

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

No balance is enforced yet. Once every converter groups its legs, the postings of a
group are required to sum to zero.

## Naming a group on upload

An uploaded tx carries an optional `group_ref`: an opaque key, scoped to that
upload, naming the event the posting belongs to. Txs sharing a non-empty `group_ref`
are stored in one group; an empty one means the tx is its own single-posting group.
The group takes the timestamp of the first leg that names it.

`group_ref` is not stored and carries no meaning across uploads, so re-uploading a
period produces new groups. This follows from transactions having no natural key
(see adr/0002-transaction-ingestion-model.md): there is nothing stable to key a
durable group identity on.

Single-transaction uploads ignore `group_ref`. One tx has nothing to group with.

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
