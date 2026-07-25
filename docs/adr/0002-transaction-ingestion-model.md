# Transaction ingestion and idempotency model

Bulk uploads (UpsertTxs) are idempotent by **replacement**: the transactions for
a given broker and period are entirely replaced by the uploaded set, never
merged. Broker statements are the authoritative record and re-uploading a period
is the natural correction workflow, so merging would risk silent duplication and
demand a reliable transaction identity we do not have.

Single-transaction uploads (CreateTx) are **append-only**: each call inserts a
new row with no deduplication. A duplicate submission therefore double-counts;
scripts must avoid resending. We accepted this over server-side dedup because
broker notifications carry no reliable natural key, and a client that needs to
fix a failed CreateTx can re-ingest the covering period via the bulk
replace path instead of retrying the single call.

Transactions have **no uniqueness constraint / natural key**. Broker statements
often supply only a date (not a full timestamp), so no reliable key exists; the
system does not enforce uniqueness. Relatedly, an empty `account` string is
valid, because some brokers and statement formats do not distinguish accounts and
an empty account represents the default/only account for that broker.
