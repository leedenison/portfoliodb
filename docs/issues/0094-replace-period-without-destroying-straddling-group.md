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
survivors' residual rather than deleting the group. `weight` and `weight_commodity`
are stored columns, so the residual is `SUM(weight) GROUP BY weight_commodity` over
the surviving postings. The surviving legs are left exactly where they are, so nothing
derived is derived again.

What survives is stated once, as the predicate every statement in the operation is
expressed against: a posting survives unless it is a routed counterparty, or a
non-synthetic posting of this upload's own broker inside the period. Two of those are
corrections to the whole-group delete -- another broker's leg is not this upload's to
replace, and a synthetic INITIALIZE posting belongs to the declaration machinery.

The routed leg takes the account type `routeResiduals` would give it --
`TRANSFER_CLEARING` for a journal, `IMBALANCE` otherwise -- rather than always
`IMBALANCE`. The one group shape that straddles today is the deposit run, which is
transfer family; a plain `IMBALANCE` would hide the surviving half from the transfer
matcher, which keys strictly on `TRANSFER_CLEARING`.

The tolerance does not carry over. A residual left by a cut is the value of the legs
the replace removed rather than the source disagreeing with itself, so it is never
`SOURCE_ROUNDING` however small it is.

`check_tx_group_balance()` is `DEFERRABLE INITIALLY DEFERRED`, so the delete and the
routed insert commit together and the group is never observed unbalanced.

## Consequences

The result is stable under repetition rather than identical to what came before:
re-importing a period leaves the out-of-period legs where they were and the
in-period legs in a new group, each balanced by a routed residual. Two groups
where a converter once wrote one. Rejoining them is what the grouping engine in 0097
is for, and it repairs these retroactively because it works over stored postings; 0095
is the half of that needing a person's judgement.

This fixes broker statement uploads as well as archive imports; both paths go
through `ReplaceTxsInPeriod`.

The delete half has landed. Rebuilding the group around its survivors was tried on the
way and abandoned: creating a new group and moving the legs into it should have let the
cascade replace four hand-written statements, but measured it swapped four for four and
added id-mapping machinery, churned group ids for nothing, and put the survivors through
an insert that reseeds the split-adjusted columns from the raw ones. The tests written to
catch that last one are kept.

The export half is what remains.
