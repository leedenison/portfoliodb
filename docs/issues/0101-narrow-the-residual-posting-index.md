---
status: closed
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

Closed. `idx_txs_residual_postings` is now `(timestamp) WHERE account_type IN
(...)`: the partial predicate isolates the residual minority, `timestamp` is the
only column the report actually filters on, grouping cannot be index-ordered
because two keys come from the joined `instruments` table, and a covering shape
would need `split_adjusted_quantity` and `group_id` -- more bytes than the heap
fetch it would save.

The audit found two further indexes worth touching in the same PR, both a
one-line SQL change in the same file:

- `idx_instrument_identifiers_lookup (identifier_type, COALESCE(domain,''), value)`
  had the anti-pattern outright: no query filters on `domain` and no query
  filters on the full triple. The domain-aware paths are already served by the
  partial unique indexes at `(identifier_type, value) WHERE domain IS NULL` and
  `(identifier_type, domain, value) WHERE domain IS NOT NULL`, and every
  reachable consumer (FX pair, ticker, ISIN) filters `(identifier_type, value)`.
  Narrowed to `(identifier_type, value)`.

- `idx_prov_instr_ident_lookup (provider, identifier_type, value)` was a related
  but different shape: no query in the codebase filters by that prefix at all.
  Both reads on the table lead with `instrument_id` and are served by the two
  partial unique indexes. Dropped; the comment describing it as a reverse
  lookup describes a lookup nothing performs.

Nothing else surfaced. `idx_txs_user_broker_time` uses its middle `broker` as a
real filter and grouping key; `idx_tx_groups_user_time` wastes a trailing
`timestamp` but is at the trailing edge, not blocking a prefix, and is too
small to be worth touching here. The remaining indexes are single-column or
partial-unique whose column order is dictated by the uniqueness they enforce.

Confirming an actual planner improvement was out of scope: it needs a seeded
dataset with a realistic residual-to-`USER` ratio, and `EXPLAIN` against the
integration-test database proves nothing either way. The correctness of the
change rides on `make test`.
