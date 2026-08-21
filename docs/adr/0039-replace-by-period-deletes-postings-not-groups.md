# Replace-by-period deletes postings, not groups

Bulk upload is idempotent by replacement
([0002](0002-transaction-ingestion-model.md)). Deleting whole tx groups, selected by
an `EXISTS` over the postings inside the window, is wrong on a group whose postings
straddle the boundary: one posting inside the window destroys legs outside it, and
the upload that triggered the delete holds only the rows its own period covers, so
nothing re-inserts them.

A group's postings need not share a timestamp, and one grouping rule spans days
deliberately: the deposit-run pass keys on reference proximity rather than on the
date bucket, because a run in the sample export settles across two days. Widening
the window until it holds whole groups is the alternative, and
[0040](0040-delete-window-widens-only-to-dataset-coverage.md) records why it is not
available. So the replace cuts, and has to leave both sides standing.

**The replace deletes the postings inside the period.** A group left with nothing
goes with them, which is every group that did not straddle, so the ordinary case is
unchanged. A group left with something keeps it and gets a counterparty routed for
what those postings no longer balance to. Balance is checked on the **stored**
weight ([0029](0029-posting-weight-is-stored.md)), so the residual is
`SUM(weight) GROUP BY weight_commodity` over the survivors and no weight rule is
re-derived. `check_tx_group_balance()` is `DEFERRABLE INITIALLY DEFERRED`, so the
delete and the routed insert commit together and the group is never observed
unbalanced.

The surviving legs stay on the group they were already in. Rebuilding the group
around them -- creating a new group and moving them into it -- removes four
statements and adds four, plus the machinery to mint ids and map old groups to new.
It also churns group ids for no gain, and puts the survivors through an insert,
where `default_split_adjusted_tx()` would reseed `split_adjusted_quantity`,
`split_adjusted_unit_price` and `share_count_basis` from the raw columns -- invisible
until an instrument had split.

## What survives a replace

A posting survives unless it is a routed counterparty, or a non-synthetic posting of
that upload's own broker inside the period. Stating it once, as the predicate every
statement in the operation is expressed against, is what keeps the delete and the
re-balance from disagreeing about it.

Two of those cases correct the whole-group delete. A posting of another broker is
not this upload's to replace. A synthetic `INITIALIZE` posting is the declaration
machinery's rather than ingestion's, and the cascade only ever took it by accident.

The surviving group's own routed legs are re-derived rather than kept: they are
deleted with the in-period postings and the whole remainder is routed fresh. A
residual carried over would be a classification of a group that no longer exists in
that shape, and a group cut twice would accumulate one residual per cut.

## The routed leg is classified by family, but not by tolerance

The counterparty takes the account type the ingest balancer would give it --
`TRANSFER_CLEARING` for a journal, `IMBALANCE` otherwise -- rather than always
`IMBALANCE`. The one group shape that straddles today is the deposit run, which is
transfer family; a plain `IMBALANCE` would hide the surviving half from the transfer
matcher, which keys strictly on `TRANSFER_CLEARING`. That rule therefore has to be
the same on both paths, and it lives in `server/residual`. Writing the transfer
tx-type set into SQL instead would make the whole operation expressible as
statements, but it is the second copy [0029](0029-posting-weight-is-stored.md)
rejected for the weight rules: a copy that has drifted checks the wrong rule while
looking authoritative.

What does **not** carry over is the tolerance. At ingest, a residual below it is the
source disagreeing with itself and is routed to `SOURCE_ROUNDING`
([0024](0024-group-balance-is-checked-on-weight.md)). A residual left by a cut is
the value of the legs the replace removed; it can be any size, and a small one is
small by coincidence. Calling it `SOURCE_ROUNDING` would assert something about the
source that is false, and would file it below the size at which anything looks
again.

A third account type meaning "cut by a period boundary" was rejected. It reads as an
instruction to restore what was there, which is stale advice: the re-uploaded rows
may genuinely differ, so a different grouping may be the right answer. The signal
would also be unreliable in both directions, since an imbalance that straddles an
upload boundary is artificial in the same way and indistinguishable from a real one.

## Consequences

Two things a whole-group delete got for free now have to be done explicitly.
`tx_groups.timestamp` is the timestamp of the first posting that named the group,
and is derived rather than data, so a group whose first leg the delete took is
re-dated to its earliest surviving posting. A transfer match is a link between two
groups ([0037](0037-transfer-matches-are-links-not-postings.md)) that used to
cascade with the group; a group that survives with a different residual has to lose
it, or the link outlives the evidence for it. Both are cheap, and the matcher runs
after ingest and rebuilds what still holds.

The result is stable under repetition rather than identical to what came before:
re-importing a period leaves the out-of-period legs where they were and the
in-period legs in a new group, each balanced by a routed residual. That is two
groups where a converter once wrote one, and the imbalance report shows two typed
residuals that net to nothing. Rejoining them is what the grouping engine in
[0041](0041-server-owns-transaction-grouping.md) is for, and it repairs these
retroactively because it works over stored postings rather than over an upload.
