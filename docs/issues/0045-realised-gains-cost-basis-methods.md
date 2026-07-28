---
status: open
title: Realised gains and cost basis methods
dependencies: [0044]
---

Compute realised gains from matched disposals, with a configurable cost basis
method.

## Motivation

Once disposals reference lots (0044), realised gain per disposal and per period
becomes computable, along with the realised/unrealised split that performance
attribution needs.

The method is not universal, and this is the part that cannot be borrowed.
Beancount's `{cost}` model is **specific identification**, which reflects US
practice. UK capital gains tax uses **Section 104 pooling**: acquisitions of the
same security are pooled at a weighted average cost, with same-day matching and
the 30-day bed-and-breakfast rule taking precedence over the pool. Beancount
handles this awkwardly, through plugins, because it cuts against its lot model.

So pooling has to be implemented here regardless of what is adopted elsewhere.
Only the data model requirement is shared, which is why 0044 is separable and
comes first.

## Design

- Method configurable rather than hard-coded, since it follows the user's
  jurisdiction: specific identification, FIFO, and Section 104 at minimum.
- Section 104 needs same-day matching first, then the 30-day rule, then the
  pool -- ordering matters and is the usual source of error.
- Report realised gains per disposal and aggregated per tax year, with the
  matching decisions visible so a user can check them.
- Unrealised gain is the complement: current value less remaining basis.

## Note

This unlocks the performance work in the unscheduled milestone list (TWR and
MWR). MWR additionally needs the external/internal cash flow boundary, which
comes from double-entry (0036 onward), not from this issue.
