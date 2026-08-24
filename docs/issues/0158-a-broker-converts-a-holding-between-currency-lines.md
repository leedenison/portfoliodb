---
status: open
title: A broker converts a holding between currency lines
milestone: M25
dependencies: [0153]
---

The `TRANSFER_LISTING` type exists and nothing produces or values one.

## Scope

Converter support, once an export in hand shows the row. Nothing in
`client/lib/csv/converters/` or `client/lib/ofx/` recognises a conversion today,
and no sample names one, so the row shape is unknown; the two legs are on one
security with opposite signs and differ only in `trading_currency`, which is the
rung that settles the line.

The netting suppression a differing pair needs. A matched pair holds its value
flat by admitting both clearing legs, which assumes both sides sit on the same
commodity at the grain being partitioned. Valuation partitions by line, so a
conversion nets to zero on the old line and zero on the new and the holding never
moves.

The same-account guard. `pairable` in `server/transfermatch/match.go` refuses any
candidate whose two sides share a broker and account, which is right for every
transfer that has two accounts, and a conversion is a movement inside one. So the
two-group form cannot pair at all, which is what keeps the suppression above
unreachable. The narrow relaxation is to admit a same-account pair only where both
sides name a line and the two differ -- an ordinary withdrawal and deposit inside
one account are on the same line or on none. `docs/spec/postings.md` states the
two-account requirement and adr/0037 assumes it; both move with the code.

## What the suppression touches

Four sites, not two. The rule is written twice and read twice.

- Valuation, user mode: the inline `EXISTS` over `transfer_matches` in
  `server/db/postgres/valuation.go`.
- Valuation, portfolio mode: the `portfolio_in_flight_txs` view in
  `server/migrations/001_initial.sql`.
- External flows reads that same view, and the user-mode mirror of the same
  `EXISTS`, in the opposite direction.

So the suppression does not go inside the shared view. A conversion moves no value
across the cash-flow boundary, and a pair the view stopped admitting would read as
a withdrawal followed by a contribution. The shape that keeps the two questions
apart is a `nets` column on the view plus a conjunct in valuation alone.

Only two known and differing lines suppress. `COALESCE(from_listing_id =
to_listing_id, TRUE)` nets, which is the `<>` semantics: a pair where either side
named no line still nets, because nothing said the holding moved.
