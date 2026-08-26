---
status: open
title: An unfiled dividend has nowhere durable to live
milestone: M23
dependencies: [0141]
---

Two kinds of dividend are fetched, refused a home in `cash_dividends`, and
recorded only as an unhandled corporate event. A **special** dividend is not the
regular series the calendar is for, and one whose **currency names no line** of
the security cannot be attributed to one (adr/0073). Neither is a provider error:
both are real payments.

0141 moved unhandled corporate events into the telemetry schema, which is
retained on a clock and is not archived. So those two now have no durable home at
all, and a rebuild loses them.

Nothing re-derives them. The fetch writes coverage for the range whether or not
the dividend was filed, and coverage *is* archived, so after a restore the cycle
believes the range is answered and never asks the provider again. The payment is
gone, and the only sign it ever existed is a telemetry row that will be purged.

The option-split unhandled events do not have this problem and are the reason the
move was safe for the rest of the table: they are re-derived from stored splits
and stored identity on every cycle.

What is open is where an unfiled dividend belongs. It is not
`cash_dividends` as that table stands -- the whole point is that it names no line
-- and a nullable line would weaken the constraint that adr/0073 exists to keep. A
row beside it, or a line-less dividend the archive carries and a person attributes
later, are both shapes worth considering.

Until then a rebuild is lossy in a way it was not before, and that is the cost
0141 accepted knowingly rather than a defect it introduced by accident.
