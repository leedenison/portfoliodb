---
status: superseded by ADR-0055
---

# Option split adjustment keys off ex_date, not knowledge time

Superseded by [0055](0055-identifier-validity-is-an-interval.md), which moves
validity onto the identifier as an interval and retires the
`instruments.identity_as_of` stamp this ADR placed and defended.

The surviving point is which clock the guard reads. OCC adjusts a contract on a
split's **effective** date, so a lookup on 1 March for a 1 June split returns the
pre-split OCC however long the split has been public. Comparing knowledge times
-- when we identified the option against when we learned of the split -- marks
that identity already-correct and skips an adjustment it needs. The comparison is
against `ex_date`, which does not move; `stock_splits.first_known_at` does, and
is not read for option adjustment.
