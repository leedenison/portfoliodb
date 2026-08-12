---
status: closed
title: Match or unmatch a transfer by hand
milestone: M12
dependencies: [0068]
---

Subsumed by 0095.

Pairing two sides of a transfer by hand is the transfer case of the one writer 0095
builds: a synthesised token with `MATCH_EXACT` and `SCOPE_USER` on each side, which
the matcher reads and turns into a link. The same writer serves the non-transfer
repair, so there is nothing separate left here to build.

Breaking a pair the matcher got wrong is not attempted, and the design settled here
is why rather than merely when. A hand-made assertion is a must-link, so it can only
ever add; the engine recomputes a neighbourhood from scratch on every cycle, so
deleting a link it derived is undone by the next run. Recording a negative would need
a mechanism this design does not have. 0095 records it as a stated non-goal.

This issue's own design is also out of date and should not be read as current: it
rested on the matcher only ever inserting, which stopped being true when 0097 landed
a regroup that drops the matches naming any group it repartitions.
