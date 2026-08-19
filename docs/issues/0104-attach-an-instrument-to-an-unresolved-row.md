---
status: open
title: Let a user attach an instrument to a row an import could not resolve
milestone: M21
dependencies: [0066]
---

Give a user a way to find an instrument and attach it to a row the system could
not identify, from the surface that reports the failure.

## Motivation

An identifier the system cannot take back is a dead end for the user today. A
transaction lands on a BROKER_DESCRIPTION placeholder and an identification
error; a declaration in a user archive is rejected outright, because a statement
about a holding the system cannot name has nothing to pad and nothing to check
(0076). Either way the only repair is to edit the file by hand and import it
again, and the file is often not the thing that is wrong -- the identifier is
real and the instrument is present, and the resolution path simply cannot get
from one to the other.

The user can already find the instrument: the declaration form searches them.
What is missing is the attachment -- searching from the error and saying "this
one", so the row resolves and the surface that reported it closes.

M21 is where a person repairs what the engine cannot derive, and this is the
same shape as the grouping and transfer-pairing repairs.
