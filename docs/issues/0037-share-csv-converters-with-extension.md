---
status: open
title: Make CSV converters and gRPC-Web transport consumable outside the client
milestone: M12
---

Split the pure Fidelity converter out of `fidelity.tsx` into `fidelity.ts`, leaving
the React options component and the registry call behind, so a non-React consumer
can import the converter without pulling in React. Export the broker transaction
type map so consumers can report which specific types were unrecognised.

Add an optional headers argument to `unaryFetch` so a caller can send
`Authorization: Bearer`, which the transport currently has no way to express.
