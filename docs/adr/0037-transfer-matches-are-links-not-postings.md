# A transfer match is a link between groups, not a posting

The two sides of a journal are balanced against `TRANSFER_CLEARING` and are
deliberately not paired at ingest (see
[0022](0022-typed-per-account-cash-flow-boundary.md)), because a broker reports them
in separate statements and sometimes in separate imports. Pairing them afterwards is
a correctness requirement rather than housekeeping: until a pair is matched, a
transfer between two accounts of one portfolio reads as a withdrawal followed by an
unrelated deposit, and money-weighted return is wrong for any multi-account
portfolio. It is also what makes a residual clearing balance mean an unmatched
transfer rather than any transfer at all.

A match is recorded as a row in `transfer_matches` naming the two tx groups, the
commodity, how they were matched and when.

## It records which account holds the other side

Not merely that a match exists. The portfolio membership test in
[0022](0022-typed-per-account-cash-flow-boundary.md) asks whether *both* accounts are
members before it nets a pair or values what is in transit, and only the identity of
the other group answers that.

## A link, not a status column and not a third group

A status column on the posting could say a side was matched but not what it matched
with, which is the one thing the consumer needs. A synthetic third group closing both
sides out would fabricate an economic event that never happened and have to be
unpicked on every rematch.

Both are also unreachable rather than merely undesirable. `check_tx_group_balance()`
enforces the group invariant at COMMIT, so a side cannot be cleared by mutating or
deleting its clearing leg: that would leave its group unbalanced. Only a whole group
deleted passes vacuously. The invariant that makes ingestion trustworthy is the same
one that rules out every representation except a link beside the ledger.

## A link rather than a merge, and that part is about transfers only

The argument above rules out a status column and a third group for any residual. The
choice of a *link* over a *merge* is narrower than that, and turns on something only
transfers have: the link records which account holds the other side, because the
membership test in [0022](0022-typed-per-account-cash-flow-boundary.md) asks whether
both accounts are members before it nets a pair or values what is in transit. Merging
two transfer groups would erase the account boundary money-weighted return depends on,
which is the whole reason the pairing exists.

An `IMBALANCE` residual carries no such boundary. Both halves of a split trade sit in
one account, and the residual is an artefact of the split rather than an economic fact,
so the right repair there is a merge: reassign `group_id` and delete both residuals.
The unreachability argument above does not forbid it -- that argument is about clearing
one side of a pair by mutating its leg. `check_tx_group_balance()` is `DEFERRABLE
INITIALLY DEFERRED`, so a merge performed in one transaction is evaluated at COMMIT,
where the merged group balances. See
[0095](../issues/0095-match-imbalanced-groups-that-should-be-one.md).

## Keyed per (group, commodity), and directed

Balancing emits one residual per commodity, so an unpaired `JRNLSEC` group can have a
security side and a cash side in flight independently, each pairing with a different
group. "The two group ids" is therefore not a key; `(group, instrument)` is, which is
what the two unique indexes -- one per direction -- state.

The link is directed: `from_group_id` is the side the value left, whose clearing
residual is positive because the group's own leg is negative. The direction costs
nothing to record, being the sign of the residual, and a link without it would read
identically for a transfer in either direction.

## Derived and disposable

A re-upload replaces one side's groups
([0002](0002-transaction-ingestion-model.md)), the cascade takes the link with them,
the surviving side reappears as unmatched, and the matcher pairs it again. Nothing
has to know a re-upload happened. The link is never authoritative and is always cheap
to rebuild, which is what licenses a heuristic to write one at all.

The matcher only ever inserts. It never updates and never deletes, so a `MANUAL`
match survives every rebuild the matcher performs.

It does not survive a regroup. Under
[0041](0041-server-owns-transaction-grouping.md) the partition is recomputed and group
ids churn, so a link keyed on them breaks whether or not the matcher touched it. For a
heuristic match that costs nothing -- it is cache, and the next cycle rebuilds it from
the same evidence. A `MANUAL` match cannot be rebuilt from evidence, which is precisely
what made it manual. A person's judgement therefore has to be recorded as an *input* to
grouping and matching and replayed on every run, keyed on something that outlives the
groups it currently names. What that key is remains open, because a posting has no
natural key ([0002](0002-transaction-ingestion-model.md)). See
[0095](../issues/0095-match-imbalanced-groups-that-should-be-one.md) and
[0091](../issues/0091-manual-transfer-match.md).

That question is settled in [0049](0049-a-human-assertion-is-a-correlation.md), and in
two parts, because the paragraph above conflates a hand-made *grouping* with a hand-made
*match*. A grouping needs no key at all: it is recorded as a correlation on the postings
it names, so it is already attached to them. A match keeps its group ids and relies on
the engine writing only the groups it disagrees with, so an id survives every cycle that
does not genuinely repartition it
([0047](0047-grouping-runs-as-precedence-ordered-passes.md)). What remains true is that
a re-upload takes both with the postings, and that this is reported rather than
repaired.

## Ambiguity is left unmatched

Two transfers of the same amount between the same pair of accounts in the same window
are not distinguishable, and surfacing both as unmatched is better than pairing them
arbitrarily. A wrong pair is worse than no pair: it looks authoritative, it points at
the wrong account, and the report that exists to find missing sides stops finding
them.

So a pair is matched only on evidence that identifies the *occurrence*: the source
naming the other account, or the proximity of the two sides' broker references. There
is no amount-and-window-alone rule. Every transfer in the sample data is intra-broker
with both sides in one import; the cross-broker case that
[0022](0022-typed-per-account-cash-flow-boundary.md) designed `TRANSFER_CLEARING` for
is real in principle but has no sample, so it cannot be calibrated and stays
unmatched and visible until one exists.

An explicit pointer is narrowing evidence, not a decision. It names the counterparty
*account*, not the occurrence, so it still needs the window and the ambiguity test:
the sample's monthly fee transfers move the same two amounts between the same account
pair every month, which a pointer plus an amount matches twelve times a year. It is
also thinner than it looks -- in the sample the field is populated on 40 rows, of
which 38 are service fees naming the account a fee was charged for, which is
attribution and not a counterparty. The reference is what carries the data.

## The server may pair groups, though it may not derive a leg

[0021](0021-converters-own-transaction-grouping.md) says the converter owns grouping
and the server never pairs rows. Matching does not contradict it.

0021 is about the server fabricating *ledger content*: a derived cash leg that would
double-count against a row the broker already reported. A match creates no posting and
no group, and corrupts no balance if it is wrong -- it degrades a netting decision,
and it is disposable. That asymmetry is what makes one acceptable server-side and the
other not.

0021's own argument for converter-owned pairing was that "by the time the data reaches
the standard format the broker's own reference numbers have been discarded -- only the
converter still has them." That premise no longer holds: the reference is stored on
the posting. And the pairing it enables spans uploads, statements and accounts, which
is a scope no single converter has. Where the evidence is within one file the
converter still does the pairing; this is the residue it cannot see.

That last sentence has since been overtaken:
[0041](0041-server-owns-transaction-grouping.md) moves the grouping decision to the
server outright, for the same reason set out here and generalised. This section stands
as the first case of it rather than as an exception to 0021.

## Consequences

`TRANSFER_CLEARING` becomes a signal. An unmatched balance means a side whose pair has
not arrived, its age is the age of something missing, and the transfers report can
drop the caveat that it lists every imported transfer.

What consumes a match is a separate change. Netting matched pairs in portfolio cash
flows, and including matched in-flight value in valuation when both accounts are
members, both read this table and neither lands with it.

Both have since landed ([0090](../issues/0090-net-and-value-matched-transfers.md)), and
they read the link the same way: through the counterpart group's own `TRANSFER_CLEARING`
leg rather than through the group. That leg carries the counterpart account and the
commodity the match is keyed on, so testing it against a portfolio's filters asks the
same question from either side of a pair, and the pair is admitted whole or not at all.
The identity this ADR says the link records is consumed exactly as claimed, by two
callers rather than one, and through a single view -- `portfolio_in_flight_txs` -- so
that valuation and the flow query cannot disagree about which pairs net.

Matching is a post-ingest job rather than part of ingestion, because the second side
can arrive in a later import: it is a function of all stored state, not of one job's
payload.

Two things signal it, on one buffered channel, as the price fetcher has. An admin RPC
is what an external cron job or CLI calls, and is how matching runs on a cadence --
there is no clock in the process. Ingestion fires the same trigger once an import
commits. Neither is redundant: a cadence alone would leave a just-imported transfer
reported as unmatched until it came round, and the ingestion nudge alone would never
retry a cycle that failed. Running it more often than needed is cheap, since a cycle
reads every side no match names and writes nothing when there is nothing new.

## Amendments

**A `MANUAL` match is derived too, and is not keyed on group ids.** "Derived and
disposable" above says a `MANUAL` match "cannot be rebuilt from evidence, which is
precisely what made it manual", and concludes that it keeps its group ids and relies
on the engine writing only the groups it disagrees with. Both halves are wrong now.

It can be rebuilt from evidence, because a person supplies some. A hand-made pairing
is recorded as a `SCOPE_USER` correlation on the two sides' postings, exactly as a
hand-made grouping is, and the matcher gains a pass above the pointer pass that reads
it. `MANUAL` stops meaning "inserted by hand and preserved" and starts meaning
"derived from a person's assertion" -- which puts every method in the table on the
same footing, and makes the link disposable in the same way. See
[0049](0049-a-human-assertion-is-a-correlation.md) and
[0095](../issues/0095-match-imbalanced-groups-that-should-be-one.md).

It also had to change, rather than merely being an improvement. The id-keyed link was
never as durable as this ADR assumed: `applyChanges` drops every `transfer_matches`
row naming a group a posting left or the engine created, and nothing rebuilt a
`MANUAL` one. So a hand-made link was already destroyed by any genuine repartition of
either side, silently.

**"The matcher only ever inserts" no longer holds either.** It was true of the
matcher and false of the system: the regroup and the period replace both delete
matches naming a touched group. What makes that safe is the rebuild above, not the
matcher's restraint.

The rest of this ADR stands. A match is still a link rather than a merge for
transfers, and still records which account holds the other side, for the reason "A
link rather than a merge" gives; what changed is where the `MANUAL` link comes from,
not what it is.
