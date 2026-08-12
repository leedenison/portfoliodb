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
justified against measured data -- 21 deposit runs, sells 88/88, buys 92/92 -- and
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

## Amendments

**The engine writes its disagreements, not its partition.** "A regroup must
re-route inside the transaction that changes membership" above says what happens
when membership moves, and leaves the impression that recomputing the partition
means rewriting every group in reach. It does not. The engine compares its
partition against the stored one and issues statements only where the two differ;
a group it agrees with keeps its id, its residual and the `transfer_matches` rows
keyed on it.

That is the cheaper implementation as well as the safer one, but it is recorded
here because something depends on it. The grouping job runs after every import,
over a neighbourhood deliberately wider than the uploaded period
([0097](../issues/0097-server-side-transaction-grouping.md)), so an engine that
rebuilt the neighbourhood each cycle would churn ids for postings nobody
uploaded, and would destroy a hand-made transfer match every time an unrelated
month was imported. [0049](0049-a-human-assertion-is-a-correlation.md) leans on
this: it lets a manual transfer match stay keyed on group ids rather than
needing a durable anchor of its own.

**Writing only its disagreements is not the same as not being allowed to
disagree**, and the distinction is load-bearing. Every stored group balances --
`check_tx_group_balance()` sees to that, and whatever is left over is routed to an
explicit residual -- so "balanced" cannot be what marks a group as settled. Nor
can "balanced with no residual worse than `SOURCE_ROUNDING`", tempting as it is,
because that is exactly what a converter's *wrong* pairing looks like: two legs
whose amounts happened to agree closely enough, joined when they should not have
been, or one leg joined to the wrong counterpart of two similar trades on the same
day. Those are the errors the engine exists to correct, and after
[0098](../issues/0098-retire-converter-side-grouping.md) there is no converter
partition left to defer to in any case.

It would also not survive the order data arrives in. A fragment that balances on
its own today may be one whose third leg arrives next month, and a rule that
forbade disturbing it would stop the engine doing the one thing
[0041](0041-server-owns-transaction-grouping.md) built it for.

So the two things a balance test looks like it offers are supplied elsewhere and
better. The *protection* is precedence: a group whose members carry a `SCOPE_USER`
correlation ([0049](0049-a-human-assertion-is-a-correlation.md)) is claimed by the
highest-precedence pass as a must-link, so no later pass can take a member from
it, and that holds whether or not the group balances -- which matters, because a
person may well assert a grouping precisely because the legs do not. The
*efficiency* is where the neighbourhood is seeded: starting from postings whose
groups carry an unresolved residual is a sound and cheap way to choose where to
look. Neither may become a rule about what the engine is permitted to conclude
once it is looking, or it can only ever repair and never correct.

**Precedence is data on the rule, not the order of its call site.** "Expect
pressure toward a per-broker precedence list" above is much easier to satisfy if
each rule carries a number than if the ordering lives in the shape of the code: a
broker's ordering becomes a table that can be stated, tested and diffed rather
than a restructuring.

Execution stays a loop over rules in that order, and the loop is both the faster
form and the only one that keeps the queries indexed. Collapsing to a single
globally sorted candidate list is equivalent -- every higher-priority candidate
sorts before every lower-priority one, and claiming is greedy either way -- but
it requires generating every rule's candidates up front, where the loop lets each
rule's claims shrink the pool before the next rule generates anything. The
predicates are also too unlike each other for one query to serve them: token
equality, amount equality, a fee-direction inequality, a directed ordinal span.

**Claims stay irrevocable, and recomputation is what makes that affordable.**
With [0050](0050-grouping-recomputes-a-neighbourhood.md) the engine partitions a
neighbourhood from scratch rather than adjusting what is stored, so a claim is
irrevocable within a run and no run inherits another's claims. The case that
looks like it needs revocation -- a leg paired on a wide-but-passing gap whose
true counterpart arrives later -- is answered by the global ranking above, which
sees both candidates at once.
