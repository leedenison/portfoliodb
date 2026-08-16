---
status: closed
title: The Fidelity converters name the trade a charge was levied on
milestone: M15
dependencies: [0110]
---

Emit a `MATCH_ATTACHES` pointer from a per-trade charge to the trade it belongs to,
from the broker's own fee schedule.

## Motivation

Nothing in a Fidelity export links a charge to a trade. The CSV names no order id,
and the charge references are a separate number series from the trade references --
167614596 beside 563466632 in the sample, some 400 million apart -- so proximity says
nothing. The richer JSON the extension reads carries `linkedAssetIsin`, which is
exactly the field that would say it, and Fidelity leaves it empty on charge rows.

What is left is the fee schedule, which is a fact about the broker rather than about
the rows: a fixed dealing fee per listed trade, 7.50 now and 10 before, and a fixed
PTM levy. That belongs in the converter, which is the only thing that knows it.

## Design

Within one account and order date, per charge type: where the charges of that type
are all one amount and their count equals the number of trades in an instrument
Fidelity charges to deal in, that is one charge each, and each gets a pointer at its
trade's own reference.

Per charge type rather than across the bucket, because a bucket mixing a fixed
dealing fee with a proportional stamp duty is not one set of equal amounts; testing
them together strands every dealing fee sharing a day with a variable charge.

A ticker in the CSV, and a valid ISIN in the JSON, is what says an instrument is
chargeable -- Fidelity writes neither for an unlisted fund, which is free to trade.

Where the counts disagree the converter says nothing: a count that happens to
balance is a coincidence rather than evidence.

Shared between `fidelity-csv.ts` and `fidelity-json.ts`, as `fidelityRefCorrelation`
already is, so the two readings of a Fidelity export cannot disagree.

## Measurements

Counts agree in 30 buckets of 30 over the 689-row export, once the cancelled rows
the converters already drop are out. Over the 298-row master in `local/masters/`, the
converter attaches 37 of 53 non-zero charges; an independent implementation of the
same argument over the same file gives 37, so the two agree exactly.

## Consequences

The `server/grouping/testdata/` goldens are unmoved, and cannot exercise this. They
were extracted from postings rather than from a broker file, so the broker wording a
charge type is named by is not in them and the count argument cannot be re-derived
over them -- which is the same reason this lives in the converter. The end-to-end
evidence is the real-data run above; the rule consuming the pointers is tested in
0110.

Disposals are still unattributed. Fidelity reports gross proceeds, so a sale offers
no figure to check an attribution against, and no count resolves a charge that sits
with several sales. 0112's amount evidence does not reach them either.
