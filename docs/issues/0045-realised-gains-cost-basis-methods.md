---
status: open
title: Realised gains and cost basis methods
milestone: M19
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
  jurisdiction: specific identification, FIFO, and Section 104 at minimum. It
  parameterises the derivation rather than being stored on a lot, so changing it
  rebuilds the lot set; 0044 records which rule produced each match.
- Section 104 needs same-day matching first, then the 30-day rule, then the
  pool -- ordering matters and is the usual source of error. The 30-day stage
  looks **forward**, at acquisitions in the 30 days after the disposal, so a
  disposal's matching is not final when it is first recorded and a later
  acquisition restates it.
- Report realised gains per disposal and aggregated per tax year, with the
  matching decisions visible so a user can check them.
- Unrealised gain is the complement: current value less remaining basis.

## What is reported

An **estimate of the taxable gain**, never a liability. Nothing here computes a
rate, an annual exemption or an amount owed, and the reporting says so. That
posture is what keeps 0054 validly closed: it was declined on the grounds that
PortfolioDB does not report figures to a tax authority, and an explicitly
non-authoritative estimate does not disturb that.

It follows that a period is allowed to have no figure. Where a disposal draws on
a lot whose cost is unknown (0044), the period carries a statement that the
taxable gain could not be calculated because information is missing, naming what
is missing, rather than a number computed over a fabricated basis. Under
Section 104 that matters more than it does per-lot: an unknown contribution makes
the *average* unknown, so it reaches every later disposal from that pool and not
only the ones matched to it. The pool therefore carries its unknown-cost quantity
as a figure of its own, so a disposal can report the proportion of its basis that
is missing instead of collapsing entirely.

Two things this needs exist nowhere and have no issue of their own yet:

- **A per-account tax treatment.** An ISA or a SIPP is outside CGT entirely and
  must be excluded from the pool. Accounts are implicit today -- distinct
  `(broker, account)` pairs in `txs` -- and 0037 deferred an accounts table to
  0046, which says of itself that it is not urgent and possibly not correct.
- **A stored jurisdiction and tax-year calendar.** The method follows the former,
  and the UK year begins on 6 April against the US calendar year.

Neither blocks 0044. Both block reporting a per-tax-year figure that is right for
a user with a tax-wrapped account.

## Currency

`weight` is denominated in each posting's settlement currency, so a GBP pool over
a security bought in USD needs an FX conversion per acquisition. That divides,
which puts a pooled cost for a foreign-currency security past the exactness
boundary regardless of the arithmetic below. Say so where the figure is reported
rather than presenting it as exact.

## Exactness

The methods differ here too. Specific identification and FIFO only add, subtract
and multiply, so a realised gain computed under either is exact, now that 0042
has made quantities and prices exact decimals. Section 104 pooling averages, and
an average divides.

The rounding that follows does not have to accumulate, though, and the way to
stop it belongs with the pool's data model rather than being discovered
afterwards. **Store the pool as `(total_qty, total_cost)` and never as a cost per
share.** A per-share average is not generally representable, so storing one
rounds on every acquisition and carries the error forward in a running balance --
which is the shape a later change cannot revise cleanly. Storing the two totals
instead means acquisitions only ever add, which is exact, and a disposal computes

    cost_out = total_cost * qty_out / total_qty

multiplying first and dividing once. The remainder stays in the pool exactly, so
there is one rounding per disposal rather than a running one. This is also the
order HMRC's worked examples use. See
adr/0026-exact-decimals-bounded-by-closure.md.

## Note

This unlocks the performance work in the unscheduled milestone list (TWR and
MWR). MWR additionally needs the external/internal cash flow boundary, which
comes from double-entry (0036 onward), not from this issue. Both metrics are
past the exactness boundary regardless of method -- geometric linking and an
internal rate of return are division and root-finding.
