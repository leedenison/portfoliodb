---
status: superseded by ADR-0056
---

# Share count basis is a convention, not a field

Superseded by [0056](0056-a-relaying-source-cannot-convert-back.md), which puts
`share_count_basis` back on all three tables. The decision below turns on every
restating source being able to convert back, and that is false for a source
which relays someone else's restatement rather than performing its own. The
identifier half of the convention -- that a file names an instrument as of its
`exported_at` -- is not affected and stands; see
[0055](0055-identifier-validity-is-an-interval.md).

A quantity of shares is meaningless without knowing which share count it is expressed in:
a 2:1 split makes "100 shares at $50" and "200 shares at $25" the same holding. So
`txs`, `eod_prices` and `holding_declarations` each carried a `share_count_basis`, the
date at which their row's share count was current. Each defaulted, by trigger, to the
row's own date, and a source that restated its data was expected to say so instead.

Nothing ever said so. Across three years of real broker exports -- four brokers, 2,892
postings -- the column was set on none of them, and no price plugin ever declared
anything but `AsTraded`, because every plugin asks its provider for unadjusted output.
[0032](0032-archive-preserves-inputs-not-derived-state.md) keeps
`split_adjusted_quantity` out of the archive as derived state, so the file carried one
number and a basis that was always the default.

What the field did do was let a restating source stay conforming while being wrong. A
row that restates and does not declare it is indistinguishable from one that does not
restate, so the failure is silent and the column that exists to catch it reads exactly
the same either way. That is what happened here: a spreadsheet built as a portfolio
tracker had multiplied two Alphabet sales into post-split terms, the archive said
nothing, and the recompute applied the split a second time.

**A source that restates knows the ratio it used, because that is how it restated.** It
can convert back. A source that does not restate does nothing. So the API fixes the
basis per row kind and requires every source to meet it:

| Row | Basis |
| --- | --- |
| A posting | its own `trade_date` |
| A price bar | its own `price_date` |
| A holding declaration | its own `as_of_date` |

Neither kind of source has to describe itself, and neither can misdescribe itself. The
knowledge stays where the work has to happen anyway, and the receiving side has one
reading of every number rather than two.

## Consequences

**The INITIALIZE pad converts.** It was the one row whose basis genuinely differed from
its own date: the pad is computed against the date its declaration asserts and written
against the date the portfolio starts, and those are the same quantity only when no
split falls between them. PortfolioDB is that row's source, so PortfolioDB converts --
`ConvertQtyToBasis` carries the declared quantity to the pad's date before the
subtraction. This is the rule applied to ourselves rather than an exception to it.

**The declaration form asks one question.** It offered "the share count on the as of
date" against "today's share count", which is a question a person either cannot answer
or can answer only by doing the conversion the form was trying to avoid. It now asks for
the quantity held on the date.

**A back-adjusted price source is now a converter's problem.** Free providers serve
adjusted series by default, so an importer that wants one must undo the adjustment
against the splits the same provider will hand it. That is more work at the edge, and it
is the edge that has the data.

## What this does not settle

Identifiers move under a split too -- an OCC symbol encodes a strike -- and an identifier
is not a quantity, so nothing above fixes their basis. The archive states identity as of
its envelope's `exported_at`; whether that is sufficient is open, and is why
[0123](../issues/0123-carry-broker-contract-identifiers.md) prefers an identifier that
does not move at all.
