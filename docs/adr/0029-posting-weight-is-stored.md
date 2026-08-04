# Posting weight is stored, not re-derived

[0041](../issues/0041-enable-balance-constraint.md) moves the group balance check
into a deferred constraint trigger. Balance is checked on weight
([0024](0024-group-balance-is-checked-on-weight.md)), whose rules -- the
converting tx types, the settlement-currency guard, the contract-size
multiplication -- live in Go, in `server/service/ingestion/balance.go`. The
trigger needs them in SQL.

Each posting **stores** the weight it contributes and the commodity it
contributes in, and the constraint is a plain `SUM(weight) = 0` per
`(group_id, weight_commodity)`. The weight rules stay in Go and are not
reimplemented in PL/pgSQL.

Two columns rather than one, because balance is per commodity: a group can leave
a residual in cash and another in a security at the same time.
`weight_commodity` is the currency code for a converted or cash leg, the
instrument for an unconverted security leg, and the posting's description when
its instrument never resolved. The fallback is a real value and never NULL, so an
unresolved posting still balances against itself.

## Considered options

- **Reimplementing the weight rules in PL/pgSQL.** Rejected on two counts. It is
  a second copy of the rules that must not drift from the Go one, which is a poor
  trade for a constraint whose value is that it cannot be bypassed. More
  decisively, it re-derives weight at COMMIT from the *current* `instruments`
  row, and instrument state moves under a posting after ingest: a merge rewrites
  `txs.instrument_id` wholesale, and `contract_multiplier` records the deviation
  a corporate action leaves behind. A re-derived check could therefore reject a
  group that was balanced when it was written, on an update that has nothing to
  do with it. A stored weight is fixed at the fact's own time, which is the same
  reason `split_adjusted_*` are stored rather than computed on read.

## Consequences

The constraint proves that the *declared* weights of a group sum to zero, not
that its postings balance. A writer that computes weight wrongly writes a
self-consistent lie, which is weaker than the unbypassable check 0041 set out to
add.

That is accepted, because it is beancount's contract as well: there, weight comes
from a `{}` or `@` annotation that whoever wrote the entry supplied. 0024 already
records the explicit annotation as correct and worth keeping open, rejected only
because nothing would balance until every converter had been taught to set it.
Storing the computed weight is that annotation arriving from the server instead
of the upload -- it buys the property without the converter work, and leaves the
door open for a converter to supply its own later.

The PL/pgSQL alternative does not deliver the stronger property either. A second
copy that has drifted checks the wrong rule while looking authoritative, which is
worse than a declared value, because the divergence is invisible.

Weight is computed from the raw `quantity` and `unit_price`, which the corporate
event recompute does not touch -- it writes only the `split_adjusted_*` pair --
so weight is written once at ingest and no recompute pass has to maintain it. The
one exception is the instrument merge, which must rewrite `weight_commodity`
alongside `instrument_id` in the same statement. Both legs of a same-instrument
group move together there, so the group stays balanced across the merge.
