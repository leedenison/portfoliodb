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

It is checked on the raw `quantity` and `unit_price`, never on the
`split_adjusted_*` pair, which carries a rounding an exact check would reject.
See adr/0024-group-balance-is-checked-on-weight.md.

## Why exact decimals first

A balance check is writable against `DOUBLE PRECISION` -- with a relative
tolerance. What that costs is a tolerance that has to be chosen and defended in
the one place whose whole purpose is to be unarguable, and an absolute epsilon
in the style of `qty_is_zero` will not do: `1e-9` is below a double's ULP at the
magnitude of a large converted weight, so it would fail on exactly the trades
that matter most. Exact decimals make the check `SUM(...) = 0` with nothing to
justify. See adr/0026-exact-decimals-bounded-by-closure.md.

Note this is separate from the ingest tolerance, which stays either way: once
residuals are routed above half a cent the group sums to zero by construction,
and that is what makes an exact constraint satisfiable.

Tracked as 0042.

## Where the weight function lives

Settled in adr/0029-posting-weight-is-stored.md: each posting stores the weight
it contributes, and the weight rules are not reimplemented in PL/pgSQL. Two
columns on `txs`, because balance is per commodity:

- `weight` -- `quantity`, or `quantity * unit_price * contract_size` for a
  converting leg. Closed under multiplication, so exact once 0042 lands.
- `weight_commodity` -- the currency code for a converted or cash leg, the
  instrument for an unconverted security leg, and the posting's description when
  its instrument never resolved. Never NULL, so an unresolved posting still
  balances against itself.

The trigger then groups on `(group_id, weight_commodity)` and checks
`SUM(weight) = 0`.

The instrument merge in `server/db/postgres/instruments.go` rewrites
`txs.instrument_id`, so it has to rewrite `weight_commodity` in the same
statement. Nothing else maintains the columns: weight is on the raw `quantity`
and `unit_price`, which the corporate event recompute leaves alone.

Note what this costs. The constraint proves that the declared weights of a group
sum to zero, not that its postings balance, so the motivation above is stronger
than what lands. 0029 records why that is the right trade anyway.

## Sequencing

Requires 0038 and 0042. Best landed after 0040, when residuals are small and
the `Imbalance` postings being written are few and explainable.
