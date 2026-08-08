---
status: open
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
