---
status: open
title: Identity predicates with no caller and no direct test
milestone: M24
dependencies: [0140]
---

Part of the proliferation is surface that nothing yet uses.

`identifier.MayMediate` is exported and has no callers. Its own comment says so:
nothing calls it until 0140, which is the rule it exists for. It carries the most
subtle condition in the package -- `systemOwned` is the caller's half of the test
and has no default, because a chain drawn through a user-owned association would
merge instance-global rows on one unauthenticated file -- and the only thing
exercising that today is its unit test.

`identifier.Venue.Known` is exported with one caller, `Permits`, in its own file.
`identifier.Venue.Agrees` is exported with one caller.

`identifier.CorroboratesSecurity` has no direct unit test. `idtype_test.go`
covers `NamesAListing`, `ProviderNamesAListing`, `Known` and `MayMediate`; this
one is reached only through `identification.corroborated`, and it is the
predicate merge admission turns on.

Either narrow the surface or give it the test that says what it promises. Resolve
after 0140, which supplies `MayMediate` its caller and settles whether the rest
of this is worth moving.
