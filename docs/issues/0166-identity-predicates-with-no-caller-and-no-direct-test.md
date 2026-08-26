---
status: open
title: Identity predicates with no caller and no direct test
milestone: M24
dependencies: [0140]
---

Part of the proliferation is surface that nothing yet uses.

`identifier.Venue.Named` is exported with one caller, `Permits`, in its own file.
`identifier.Venue.Agrees` is exported with one caller.

Either narrow the surface or give it the test that says what it promises.

What this opened with is settled. `identifier.MayMediate` has its caller in merge
admission, and the condition its comment called the subtle one has left the type
predicate: authority is not a property of a type, so the caller asks the row.
`identifier.CorroboratesSecurity` has a direct test pinning its answer per type,
written when it stopped reading scope.
