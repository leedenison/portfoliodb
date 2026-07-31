# Transaction Groups and Postings

A **tx group** is one economic event. The `txs` rows that reference it are its
**postings**. See adr/0020-double-entry-postings.md.

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

Groups currently hold a single posting each: what is stored is unchanged from before
grouping existed, and no balance is enforced. Once ingestion emits multiple legs per
group, the postings of a group are required to sum to zero.

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
