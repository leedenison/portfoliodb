# Posting weight is stored, not re-derived

Balance is checked on weight ([0024](0024-group-balance-is-checked-on-weight.md)),
whose rules -- the converting tx types, the settlement-currency guard, the
contract-size multiplication -- live in Go. The deferred constraint trigger that
enforces the group invariant needs them in SQL.

Each posting **stores** the weight it contributes and the commodity it contributes
in, and the constraint is a plain `SUM(weight) = 0` per
`(group_id, weight_commodity)`. The weight rules stay in Go and are not
reimplemented in PL/pgSQL.

Two columns rather than one, because balance is per commodity: a group can leave a
residual in cash and another in a security at the same time. `weight_commodity` is
the currency code for a converted or cash leg, the instrument for an unconverted
security leg, and the posting's description when its instrument never resolved. The
fallback is a real value and never NULL, so an unresolved posting still balances
against itself.

## The encoding

The three kinds are prefixed rather than stored bare: `cur:USD`, `inst:<uuid>`,
`desc:<description>`. Without a prefix a description that happened to read `USD`
would be the same commodity as the currency, and the merge would have no way to
rewrite instrument names without also matching a description that looked like a
uuid. It is the same string `commodity.key()` already produces to accumulate the
sums, so the stored value is the key the residual was computed against rather than
a second spelling of it.

A uuid column referencing `instruments` was rejected. Currencies are instruments,
so `cur:USD` could have been the currency instrument's id -- but that means
resolving the settlement currency to an instrument for every converted posting
rather than only for the routed ones, and it leaves the `desc:` fallback with
nowhere to go on a posting whose instrument never resolved. A nullable commodity
would then be a group the constraint cannot check.

## Weights the caller does not supply

`db.TxDB` takes weights as a slice parallel to the postings, and a nil slice means
the caller has none. Each posting then weighs its own quantity in its own
instrument, which is not a placeholder: it is exactly what the weight rule returns
for a posting with no price. Fixtures that write postings directly are therefore
writing a defensible weight rather than a filler value, and a caller that forgets
to supply weights produces a group that fails the constraint rather than one that
silently passes.

## Considered: reimplementing the weight rules in PL/pgSQL

Rejected on two counts. It is a second copy of the rules that must not drift from
the Go one, which is a poor trade for a constraint whose value is that it cannot be
bypassed. More decisively, it re-derives weight at COMMIT from the *current*
`instruments` row, and instrument state moves under a posting after ingest: a merge
rewrites `txs.instrument_id` wholesale, and `contract_multiplier` records the
deviation a corporate action leaves behind. A re-derived check could therefore
reject a group that was balanced when it was written, on an update that has nothing
to do with it. A stored weight is fixed at the fact's own time, for the same reason
`split_adjusted_*` are stored rather than computed on read.

## Consequences

The constraint proves that the *declared* weights of a group sum to zero, not that
its postings balance. A writer that computes weight wrongly writes a self-consistent
lie, which is weaker than an unbypassable check.

That is accepted, because it is beancount's contract as well: there, weight comes
from a `{}` or `@` annotation whoever wrote the entry supplied. 0024 records the
explicit annotation as correct and worth keeping open, rejected only because nothing
would balance until every converter had been taught to set it. Storing the computed
weight is that annotation arriving from the server instead of the upload, and leaves
the door open for a converter to supply its own later. The PL/pgSQL alternative does
not deliver the stronger property either: a second copy that has drifted checks the
wrong rule while looking authoritative.

Weight is computed from the raw `quantity` and `unit_price`, which the corporate
event recompute does not touch, so weight is written once at ingest and no recompute
pass maintains it. The one exception is the instrument merge, which must rewrite
`weight_commodity` alongside `instrument_id` in the same statement; both legs of a
same-instrument group move together there, so the group stays balanced across it.

Weight also does not depend on the group. Only the posting's own fields reach the
rule -- quantity, price, currencies, contract size and the transaction type -- and
[0046](0046-declared-ambiguity-is-bounded-by-weight-neutrality.md) keeps it that way
by refusing a declared type set whose members would weigh differently. So regrouping
a posting never rewrites its weight, and the deferred constraint is not disturbed by
a partition that changes underneath it.
