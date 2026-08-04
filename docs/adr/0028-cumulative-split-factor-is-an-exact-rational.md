# The cumulative split factor is an exact rational

`split_factor_at` returns the product of `split_to / split_from` over the splits
between a row's `share_count_basis` and today, and computes it as
`exp(sum(ln(...)))` on `double precision`. The function's own comment defends
that: split factors are small rationals and the round trip is accurate to many
decimal places for any realistic chain. It was a reasonable choice while the
quantities it multiplied were themselves `double precision`.

It stops being one once quantities are exact
([0026](0026-exact-decimals-bounded-by-closure.md)), and the obvious repair --
running the same expression in `numeric` -- is the worst available option.
Postgres implements `ln` and `exp` on `numeric` as software series expansions
over arbitrary-precision digit arrays, so it is far slower than the `float8`
path and the result is still approximate. Paying that cost for an approximation
makes no sense.

The factor is a product, so it is computed as one. Postgres has no built-in
product aggregate, but defining an exact one is a single statement, and
multiplication over `numeric` is exact:

```sql
CREATE AGGREGATE mul(numeric) (SFUNC = numeric_mul, STYPE = numeric, INITCOND = '1');
```

The function returns the numerator and denominator separately -- `mul(split_to)`
and `mul(split_from)` -- rather than their quotient, so callers compute
`quantity * num / den` with all the multiplication first and a single division
last. That ordering is the minimal-error form, and deferring the division is
what keeps the exact part exact for as long as possible.

## Considered options

- **`exp(sum(ln(...)))` on `numeric`.** Rejected as above: slower and still
  inexact.
- **A recursive CTE walking the split chain.** Exact, but it re-expresses a
  product as an ordered traversal for no benefit over an aggregate.
- **Storing a cumulative factor per instrument.** Rejected: it is derived data
  with a `CURRENT_DATE` dependency, so it needs invalidating every time a split's
  ex-date passes, which is precisely the maintenance the function exists to
  avoid.
- **Computing the product in Go.** Rejected: the factor is applied inside the
  recompute statements in `server/db/postgres/corporate_events.go`, so moving it
  out of SQL would mean reading the split chain into the service and pushing the
  adjusted values back, replacing a set-based update with a round trip.

## Consequences

The quotient is exact whenever the denominator divides `quantity * num`, which
covers every forward split and every ratio whose denominator factors into 2s and
5s -- 2:1, 3:2, 5:4. It is not exact for a genuine reverse `/3`: a 3:1 reverse
split on 100 shares is 33.333..., and no representation fixes that. So the
`split_adjusted_*` columns carry a declared rounding scale, chosen once and
recorded in the schema.

This is the boundary case that [0026](0026-exact-decimals-bounded-by-closure.md)
classifies by expression rather than by storage. The rounding is confined to a
derived cache: `quantity`, `share_count_basis` and the split chain are all exact
and the adjusted values are recomputable from them at any time, which is already
how the corporate-event recompute works. It also means an exact balance check
must read the raw columns rather than the adjusted pair --
[0024](0024-group-balance-is-checked-on-weight.md) records that.

See [spec/bitemporality.md](../spec/bitemporality.md#share-count-basis) for what
the share count basis is and which sources declare which.
