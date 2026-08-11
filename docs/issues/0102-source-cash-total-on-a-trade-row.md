---
status: open
title: Carry the source's own cash total for a trade row
milestone: M15
---

Transcribe the cash total a source states for a row whose own quantity is not
money, so that grouping has the figure the broker wrote rather than one derived
from two other fields.

## Motivation

adr/0041-server-owns-transaction-grouping.md moves grouping to the server, and
0097 builds the engine. The evidence the engine reads is short of what the
converter it must reproduce reads, in one specific place.

For a cash row Fidelity's `Amount` becomes the posting's `quantity`, so it
reaches the server exactly. For a security trade row it is parsed, used inside
`assignFidelityGroups`, and then discarded: the posting is built from `quantity`
and `unit_price` alone. So where the converter compares two independently
transcribed totals -- which is what its own comment says the pairing rests on,
"quantity * unit price is derived from different fields, which makes it an
independent check" -- the server has one transcribed number and one derived.

That is not broker idiosyncrasy, so it is not something a converter should be
left to work around. Every source reports a trade's cash total and rounds the
unit price it quotes, so the loss is the same everywhere: an exact equality that
identifies a cash leg degrades into a tolerance band wide enough to admit a
different trade of a similar size. It is also wrong rather than merely weaker for
Schwab, where 0073 records commission netted into `Amount` against
split-adjusted quantities and as-traded prices, so `quantity * unit_price` is not
the consideration at all. A server that derives the consideration derives a
different number per broker; a source that states it is transcribing, which is
what adr/0048-correlations-declare-their-own-semantics.md asks converters for.

## Design

`settlement_amount` on the posting: the cash total the source stated for the row,
denominated in the `settlement_currency` already beside it. Optional, and
transcribed rather than computed -- a converter that has to work it out from
other columns has inferred it and should emit nothing.

Populated only where the posting's own quantity is not money. On a cash posting
the quantity is already that total, so carrying it twice would create a second
figure to disagree with the first, and validation rejects it there.

Per converter: Fidelity CSV and the extension's Fidelity JSON already parse the
`Amount` cell; OFX already reads `TOTAL` to build the cash leg.

## Scope

It has to be stored and to travel in the archive, for the reason the correlations
do: a rebuild from an archive would otherwise leave the engine with less evidence
than the upload had.

**Weights and balancing are out of scope.** adr/0024-group-balance-is-checked-on-weight.md
weighs a `TRADE_ASSET` leg at `quantity * unit_price`, and
adr/0029-posting-weight-is-stored.md fixes that at the fact's own time. Weighing
on this field instead would move every trade group's `SOURCE_ROUNDING` residual
and is a separate argument. This is grouping evidence and a cross-check.
