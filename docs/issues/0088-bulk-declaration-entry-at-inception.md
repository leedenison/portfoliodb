---
status: open
title: Bulk entry of holding declarations at inception
milestone: M19
dependencies: [0123]
---

Let a user enter a whole account's opening balances in one pass rather than one
form per holding.

## Motivation

A pad seeds the opening balance for a holding, and a portfolio with forty
holdings is forty forms before the balances are right
(adr/0030-declarations-are-padded-then-asserted.md). The same shape recurs
whenever a statement is used to add assertions: one date, one account, several
dozen rows.

That is entry, not reconciliation. Checking a declaration against what the
transactions add up to is 0043, which already computes the holding and surfaces
the gap. This issue is only about getting the numbers in.

## Design

Open: whether this is a paste grid in the Opening Balances tab or a file import.

A paste grid keeps the instrument picker, so identity is resolved as the user
goes and there is no resolution failure to report -- which matters, because a
declaration that cannot be identified has nothing to pad and nothing to check
against. A file import reuses the archive path from 0076 but has to answer
identity for rows a user typed rather than picked.

A broker position list answers that for the import arm. An OFX/QFX `INVPOSLIST`
is already one account read at one date -- the `Statement` the declaration part
nests by -- and every row carries a `SECID`, so identity resolves from the file
rather than from a picker. `ACCTID` gives the account, `DTASOF` the
`as_of_date`, `UNITS` a signed `declared_qty` whose sign agrees with `POSTYPE`,
and `share_count_basis` stays absent because a quantity read off a statement of
that date is already denominated in the share count current then.

`client/lib/ofx/parser.ts` reads `DTASOF` only as a fallback for when the file
was written and parses no position list at all, so the IBKR converter emits
postings and nothing else. Positions are new work in the parser and in
`ibkr-ofx.ts`. Option positions are named by CONID, which is 0123.

What this buys is assertions rather than entry. A run of statements from one
account pads at the earliest and checks at every later date
(adr/0030-declarations-are-padded-then-asserted.md), and the checks mean
something only where the transaction stream between them has no gap -- so the
same files that supply the positions have to supply the transactions.

Open: an `INVPOSLIST` is a complete snapshot, an instrument absent from it being
held at zero, but `DeclarationPart` states that absence is not deletion and never
retracts. Either that completeness is dropped, or the converter synthesises
explicit zero rows for instruments the file does not mention -- a declaration the
statement does not literally make.

Split out of 0076, which covers the archive round trip.
