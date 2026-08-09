# Replace-by-period deletes postings, not groups

Bulk upload is idempotent by replacement
([0002](0002-transaction-ingestion-model.md)), and the replace originally deleted
whole tx groups, selected by an `EXISTS` over the postings inside the window. That
is wrong on a group whose postings straddle the boundary.

A group's postings need not share a timestamp, and one grouping rule spans days
deliberately: the Fidelity deposit-run pass keys on reference proximity rather than
on the date bucket, because a run in the sample export settles across two days. One
posting inside the window therefore destroyed the whole group, including legs
outside it, and the upload that triggered the delete holds only the rows its own
period covers. The legs outside were gone and nothing re-inserted them. The
extension's 14-day lookback re-fetched them by accident, which is protection sized
for a different problem and disappears when the lookback is lowered, when a user
uploads a hand-picked file, or when a straddle exceeds it.

**The replace deletes the postings inside the period.** A group left with nothing
goes with them, which is every group that did not straddle, so the ordinary case is
unchanged. A group left with something keeps it and gets a counterparty routed for
what those postings no longer balance to. Balance is checked on the **stored**
weight ([0029](0029-posting-weight-is-stored.md)), so the residual is
`SUM(weight) GROUP BY weight_commodity` over the survivors and no weight rule is
re-derived to compute it. `check_tx_group_balance()` is `DEFERRABLE INITIALLY
DEFERRED`, so the delete and the routed insert commit together and the group is
never observed unbalanced.

## The routed leg is classified by the ordinary rule

The counterparty takes the account type any residual would take -- `IMBALANCE`,
`TRANSFER_CLEARING` or `SOURCE_ROUNDING`, chosen from the surviving legs' tx types
and the tolerances in [0024](0024-group-balance-is-checked-on-weight.md) -- rather
than always `IMBALANCE`. The one group shape that straddles today is the deposit
run, which is transfer family; a plain `IMBALANCE` would hide the surviving half
from the transfer matcher, which keys strictly on `TRANSFER_CLEARING`. Using one
rule everywhere also means a group's residual does not depend on whether it was
created whole or by a partial delete.

That rule therefore has to be the same on both paths, and it now lives in one
place, `server/residual`. Writing the transfer tx-type set and the tolerances into
SQL instead would have made the whole operation expressible as statements, but it
is the second copy [0029](0029-posting-weight-is-stored.md) rejected for the weight
rules: a copy that has drifted checks the wrong rule while looking authoritative.
The SQL aggregates stored weights, which is all the constraint trigger itself does,
and Go classifies what comes back.

The surviving group's own routed legs are re-derived rather than kept: they are
deleted with the in-period postings and the whole remainder is routed fresh. A
residual carried over would be a classification of a group that no longer exists in
that shape, and a group cut twice would accumulate one residual per cut.

## Consequences

Two things a whole-group delete got for free now have to be done explicitly.
`tx_groups.timestamp` is the timestamp of the first posting that named the group,
and is derived rather than data, so a group whose first leg the delete took is
re-dated to its earliest surviving posting. A transfer match is a link between two
groups ([0037](0037-transfer-matches-are-links-not-postings.md)) that used to
cascade with the group; a group that survives with a different residual has to lose
its match, or the link outlives the evidence for it. Both are cheap, and the
matcher runs after ingest and rebuilds what still holds.

The result is stable under repetition rather than identical to what came before:
re-importing a period leaves the out-of-period legs where they were and the
in-period legs in a new group, each balanced by a routed residual. That is two
groups where a converter once wrote one, and the imbalance report shows two typed
residuals that net to nothing. Rejoining them is
[0095](../issues/0095-match-imbalanced-groups-that-should-be-one.md).
