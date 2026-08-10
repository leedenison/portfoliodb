# Grouping runs as precedence-ordered passes, and resolution is their by-product

[0044](0044-tx-type-is-declared-and-resolved.md) says grouping resolves a declared
set of candidate types. But grouping also *consumes* the type as evidence: a cash
leg is looked for beside the asset leg it settles. Read naively that is a
fixpoint -- the partition depends on the types and the types depend on the
partition -- and an implementation that treats it as a pipeline will be wrong in
one direction or the other.

**Grouping passes run in a fixed precedence order, a pass may claim a row only if
the row's candidate set admits that pass's type, and the pass that claims a row is
what resolves it.** There is no narrowing phase.

This dissolves the cycle rather than solving it. Nothing has to compare two groups
that want the same row with equal force, because the earlier pass has already taken
it -- and nothing has to revisit a type, because being claimed by the trade pass is
what makes a row a trade's cash leg. `asTradeCashLeg` performs exactly this
retyping in the converter today, from exactly this evidence; the decision moves to
the server unchanged and stops being a special case.

Two levels are required, and both are already proven in `assignFidelityGroups`:

- **Precedence between passes.** Deposit runs are built last, "only ever from cash
  rows the trade passes did not want", which is what stops a deposit taking the
  cash row of a trade of the same amount on the same day.
- **Global ranking within a pass.** Buy candidates are ranked by reference distance
  across the whole file before any claim is made, "so that one buy cannot strand
  another by claiming its cash row". Precedence between passes does not give this.

The admissibility test is [0044](0044-tx-type-is-declared-and-resolved.md)'s
*may be* predicate, doing real work: it is what stops the trade pass claiming a row
whose source said it was definitely a transfer.

## Consequences

**A residual is routed once.** Because resolution completes inside the grouping
pass rather than after it, a group's residual is classified against final types.
Were resolution a later phase, an ambiguous journal would route to `IMBALANCE`
under the every-candidate rule and then move to `TRANSFER_CLEARING` when it
resolved, and the imbalance report would churn on every regroup.

**Claims are irrevocable, so the precedence list is correctness, not tuning.** With
no backtracking, the ordering decides the partition. The converter's ordering is
justified against measured data -- 21 deposit runs, sells 91/91, buys 78/78 -- and
a single server-side ordering has no one broker's data to justify it against, so an
order that is right for one source may be wrong for the next. Expect pressure
toward a per-broker precedence list. That is
[0041](0041-server-owns-transaction-grouping.md)'s own prediction ("expect
broker-specific passes on the server, not their disappearance") arriving at the
ordering rather than at the passes, and it is not a failure of the design.

**A regroup must re-route inside the transaction that changes membership.** Group
membership, `resolved_tx_type` and the routed residual postings all move together,
and [0029](0029-posting-weight-is-stored.md)'s stored weights do not
([0046](0046-declared-ambiguity-is-bounded-by-weight-neutrality.md)). So a regroup
deletes the old residual postings and routes fresh ones in the same transaction as
the membership change; leaving them for a later statement exposes a moment where
the deferred balance constraint fires on data that was valid before the regroup
began. This applies to the grouping pass and to archive import alike, and is stated
here because it otherwise sits implicitly across
[0024](0024-group-balance-is-checked-on-weight.md),
[0029](0029-posting-weight-is-stored.md),
[0039](0039-replace-by-period-deletes-postings-not-groups.md) and
[0043](0043-grouping-does-not-travel-in-the-archive.md).

## Considered: grouping permissively, then narrowing in a second pass

Group under the *may be* reading, then narrow each set against the group it landed
in, then regroup if narrowing invalidated a decision. It is the obvious reading of
0044 and it is what a pipeline implementation would arrive at.

Rejected because the third step has no terminating argument and the second has no
tie-break. Two groups can each admit a row under the permissive reading, and
nothing in the narrowing step ranks them; the converter's answer to that has always
been to order the passes so the question never arises, and ordering the passes
makes the second pass unnecessary rather than merely cheaper.
