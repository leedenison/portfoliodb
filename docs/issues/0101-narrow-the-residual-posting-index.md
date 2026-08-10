---
status: open
title: Narrow the residual posting index and audit the index set
---

`idx_txs_residual_postings` carries five columns after its partial predicate that
no query appears to reach. Narrow it, and audit the rest of the index set for the
same pattern.

## The analysis

```sql
CREATE INDEX idx_txs_residual_postings
  ON txs (account_type, user_id, broker, account, instrument_id, tx_type)
  WHERE account_type IN ('IMBALANCE', 'TRANSFER_CLEARING', 'SOURCE_ROUNDING');
```

- The **partial predicate is the whole value**. It narrows the scan to the residual
  minority, which is the index comment's own argument and is sound.
- `account_type` leads an index already filtered to three values of it.
- `user_id` sits at position 2 and no query touches it. `residualBalanceAgg` is the
  cross-user admin report -- no `user_id` filter, no `user_id` in the `GROUP BY` --
  and it is the only consumer, used at `server/db/postgres/residual_balances.go:116`
  and `:137`. So `user_id` sits between `account_type` and the columns the report
  does group by, breaking any prefix match.
- Index-ordered grouping is impossible regardless, because the `GROUP BY` includes
  `i.name` and `i.asset_class` from the joined `instruments` table.
- It is not covering: `timestamp`, `split_adjusted_quantity` and `group_id` are all
  needed and none is in the index, so every matched row takes a heap fetch anyway.

`tx_type` at position 6 is therefore unreachable. The likely shape is
`(timestamp) WHERE account_type IN (...)`, since the report filters on a time
window.

## Scope

Confirming this needs a seeded dataset with a realistic residual-to-`USER` ratio.
`EXPLAIN` against the integration-test database will sequential-scan a small table
and prove nothing either way.

The audit should look for the same shape elsewhere: a column that no query filters
or groups on, sitting in front of the columns that would otherwise have matched a
query's prefix.
