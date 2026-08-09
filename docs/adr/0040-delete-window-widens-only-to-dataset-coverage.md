# A delete window widens only as far as the replacing dataset covers it

Bulk upload is idempotent by replacement ([0002](0002-transaction-ingestion-model.md)):
a period is deleted and the uploaded set is written in its place. A tx group can
straddle the period boundary, so the delete cuts an economic event in half. The
obvious repair is to widen the window until it holds whole groups, so that nothing is
ever cut.

**Widening is legal only where the replacing dataset covers the widened range.**
Where it does not, the replace cuts the group and the grouping pass
([0041](0041-server-owns-transaction-grouping.md)) rejoins it afterwards.

The rule matters because the delete and the data that replaces it are decided at
different times. An archive is a snapshot: a period exported at time X, in which one
transaction sat in an imbalanced group, is re-imported at time Y, by which point a
balancing leg has been appended and has joined that group. Widening the delete to the
group's extent as it stands at Y destroys the appended leg, which the archive was
never in a position to carry. Cutting preserves it, at the cost of a residual on each
side that the grouping pass then clears.

A broker statement has no choice at all: it covers the period it covers, and there is
nothing outside it to widen into.

## The one case that could widen, and why it does not

The extension picks its own window and only then fetches
(`extension/src/background/sync.ts`), so it could snap its lower bound back to a
boundary that cuts no stored group and fetch the wider range -- the one place where
widening is both legal and free of the time-skew problem above. It is not built,
because it would be a second mechanism for a problem the grouping pass already solves
everywhere else, and because it would still need a fallback for the range a broker
will no longer serve.

## Consequences

Every replace path cuts groups the same way and relies on one repair, which
concentrates correctness in the grouping pass rather than spreading it across the
callers. Until that pass exists, cut groups stay cut: a fragment and a routed
residual on each side. That is worse than a whole group and better than the silent
loss of the legs outside the window, which is what deleting whole groups did.

An export therefore adheres strictly to the period it was asked for, contributing only
the in-period legs of a straddling group, and the exported group does not balance. The
format already allows that and the importer already routes the residual.
