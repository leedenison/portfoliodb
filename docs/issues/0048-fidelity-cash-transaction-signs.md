---
status: closed
title: Fidelity converter stores cash outflows as positive quantities
---

The Fidelity CSV puts the magnitude in `Quantity` and the direction in the sign of
`Amount`. The converter reads `Quantity` and negates only for `SELL*` types, so
every cash outflow is stored as an increase: a Service Fee of -5.20 becomes +5.2,
and a matched transfer pair that should net to zero adds twice its value to the
cash holding instead.

`Quantity` is also 0 on some rows where money moved (a Tax On Interest of -0.20
carries `Quantity` 0), so for cash movements the amount has to come from `Amount`
rather than being sign-corrected.

Affects any transaction already uploaded through the web client.
