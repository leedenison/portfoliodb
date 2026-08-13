---
status: closed
title: Consolidated user export and import page
milestone: M14
dependencies: [0078]
---

One page where a user exports and imports their own archive: transactions,
holding declarations and preferences.

## Motivation

None of a user's own data can be got out today. Transactions are import-only,
declarations are created a form at a time and never exported, and preferences
exist only in the database. All of it is tier 1 in
adr/0032-archive-preserves-inputs-not-derived-state.md -- irreplaceable, and
recoverable from nowhere.

## Design

Scoped with `RequireUser`. It exports the user's own data only and contains no
system data at all (adr/0033-system-and-user-archives-are-separate.md).

A menu of what to include, mirroring the admin page from 0079: transactions
(0077), holding declarations (0076), preferences (0085).

Restoring into an instance whose instruments are not loaded is supported and
correct: postings resolve through the normal identifier path. It is slower, and
the fix is to restore the system archive first, but it is not an error and should
not be presented as one.

### Transaction upload keeps its own affordance

The existing upload modal converts a broker's own file -- Fidelity CSV, IBKR OFX,
Schwab CSV -- through a converter. That is a different operation from restoring
an archive: different input, different failure modes, different frequency. The
two should not be merged into one control, and the upload path stays where it is.

Update docs/spec/information-architecture.md with the new page and the
distinction between the two.

## What 0085 already landed

The page itself is done: `/archive`, the export and import RPCs, the shared
export menu and import panel, the per-part results table and the IA entry all
landed under 0085, which needed them to carry its own part. What remains here is
the transactions and holding declarations rows in the include menu, once 0077 and
0076 land the parts behind them.

Closed. The include menu arrived before this issue did: 0076 and 0077 each
brought their own row along with the part behind it, so all three parts were
already on the page and enabled. What closed the issue was what a *consolidated*
page claims and nothing had shown -- that the parts compose.

The whole archive now round trips as one file. Every earlier test unticked the
other two parts and proved one alone, which says nothing about what happens when
they travel together, and that is where the pads, the restore order and the
synthetic postings meet. Two things fell out of writing it.

The declarations part succeeding is the only ordering guarantee a round trip can
show. A declaration dated before the portfolio start date is rejected, and a user
whose transactions have just been deleted has no start date at all, so the part
would have failed whole had it been applied before them. Preferences-first is not
observable and was not manufactured: an instance cannot hold postings its own
ignored asset class rules cover, so no self-consistent export carries both.

The transaction part must carry six postings where the table holds ten. The pads
a restored declaration earns are synthetic, and a part leaking into another would
show up as a pad about to be re-imported as a real transaction.

The two archive pages had also written the same machinery twice -- the include
menu, the export and download, the job lookup and poll, the results card. They
now share an `ExportArchivePanel`, a `useArchiveJob` hook and an
`ArchiveJobSection`, on the split `ImportArchivePanel` already made: what a menu
means is common, and which archive is being built is not.

The information architecture claimed the transaction and declaration parts were
still to come, and described the upload modal as the thing that converts a
broker's own file. Since 0084 that modal also accepts a transactions document in
the archive schema, so the distinction that survives is scope rather than format:
one file covering one period, replacing that period, against a whole archive
restored part by part.

The export period the RPC accepts still has no control on the page. Nothing asked
for one, and 0094 settled the semantics without a UI, so it stays out rather than
arriving unasked.
