---
status: open
title: Enforce posting balance with a deferred constraint trigger
milestone: M12
dependencies: [0038, 0042]
---

Add a `DEFERRABLE INITIALLY DEFERRED` constraint trigger that checks, at
COMMIT, that the postings of each `tx_group` sum to zero.

## Motivation

Enforcing the invariant in the database rather than the application makes it
unbypassable: no code path, no bad import, and no manual psql session can leave
an unbalanced group behind. This is stronger than beancount, which validates
only at load time -- a database constraint has no equivalent of an unchecked
writer.

It is also cheap. Once 0038 routes residuals to `Imbalance`, balance is always
satisfiable by construction, so turning the constraint on cannot reject
otherwise-valid data.

## Design

```sql
CREATE CONSTRAINT TRIGGER tx_postings_balance
  AFTER INSERT OR UPDATE OR DELETE ON txs
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION check_tx_group_balances();
```

Deferral to COMMIT is required so that the legs of a group can be inserted in
any order within one database transaction. There is precedent in the schema:
`exchanges.operating_mic` and the `plugin_config` precedence uniqueness both
use `DEFERRABLE`.

Balance is checked on weight -- quantity for same-commodity postings, and
quantity times price for postings carrying a cost or conversion.

## Blocker: quantity is DOUBLE PRECISION

An exact `SUM(...) = 0` check cannot be written against
`txs.quantity`/`unit_price`, which are `DOUBLE PRECISION`. Summing float buys
and sells does not land on exactly zero -- this is already why
`qty_is_zero(q) := ABS(q) < 1e-9` exists in 001_initial.sql. Applying the same
epsilon to a balance check would defeat its purpose, since a genuine imbalance
smaller than the tolerance would pass silently and a legitimate group could
still fail as the number of legs grows.

Tracked as 0042.

## Sequencing

Requires 0038 and 0042. Best landed after 0040, when residuals are small and
the `Imbalance` postings being written are few and explainable.
