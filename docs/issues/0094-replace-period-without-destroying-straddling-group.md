---
status: open
title: Replace a period without destroying a straddling group
milestone: M14
dependencies: [0077]
---

Make a period-scoped export and a period-scoped replace safe for a transaction
group whose postings fall on both sides of the boundary.

## Motivation

A group's postings need not share a timestamp, and one grouping rule spans days
deliberately: the Fidelity deposit-run pass keys on reference proximity rather
than on the date bucket, because a run in the sample export settles across two
days. `client/lib/csv/converters/fidelity-csv.ts` says so and a test pins it.

`ReplaceTxsInPeriod` deletes whole groups, selected by an `EXISTS` over postings
inside the window. One posting inside the window therefore destroys the whole
group, including legs outside it, and the upload that triggered the delete holds
only the rows its own period covers. The legs outside are gone and nothing
re-inserts them. Today the extension's 14-day lookback usually re-fetches them by
accident; that is protection sized for a different problem, and it disappears
when the lookback is lowered, when a user uploads a hand-picked file, or when a
straddle exceeds it.

Nothing states an invariant that a group's postings lie inside the uploaded
period, nothing enforces one at ingest, and no issue or ADR discusses the case.

## Design

Export and delete are one contract, so they are settled together.

**The export adheres strictly to the period asked for.** A group straddling the
boundary contributes only its in-period legs, and the exported group therefore
does not balance. That is already legal: the format accepts a group whose
postings do not sum to zero, and the importer routes the residual. Whether the UI
should warn that a group is being split is a later question.

This fills `ExportUserArchiveRequest` fields 2 and 3, reserved by comment for the
bounds of the transaction window. 0077 exports one window per broker derived from
that broker's own data, so the window contains every posting it carries and the
case cannot arise; this issue is what makes a narrower period expressible.

**The delete removes only the postings inside the period**, and re-routes the
survivors' residual rather than deleting the group. `weight` and
`weight_commodity` are stored columns, so the residual is
`SUM(weight) GROUP BY weight_commodity` over the surviving postings and the whole
operation is expressible in SQL without lifting the balancer out of the worker.

The routed leg takes the account type `routeResiduals` would give it --
`IMBALANCE`, `TRANSFER_CLEARING` or `SOURCE_ROUNDING`, chosen from the surviving
legs' tx types and the existing tolerances -- rather than always `IMBALANCE`. The
one group shape that straddles today is the deposit run, which is transfer
family; a plain `IMBALANCE` would hide the surviving half from the transfer
matcher, which keys strictly on `TRANSFER_CLEARING`. Using one rule everywhere
also means a group's residual does not depend on whether it was created whole or
by a partial delete.

`check_tx_group_balance()` is `DEFERRABLE INITIALLY DEFERRED`, so the delete and
the routed insert commit together and the group is never observed unbalanced.

## Consequences

The result is stable under repetition rather than identical to what came before:
re-importing a period leaves the out-of-period legs where they were and the
in-period legs in a new group, each balanced by a routed residual. Two groups
where a converter once wrote one. Rejoining them is 0095.

This fixes broker statement uploads as well as archive imports; both paths go
through `ReplaceTxsInPeriod`.
