---
status: closed
title: The UI says which grain it means
milestone: M25
dependencies: [0148, 0149, 0150]
---

A user filtering on a company means the security; a user reconciling a statement
means the line. Each surface picks one and has to show which it picked.

## Scope

`client/app/admin/instruments/page.tsx` shows listings under a security rather
than an exchange and a currency inline on the row. Holdings and
`declaration-form.tsx` pick and show a listing, defaulting to the sole one where
a security has only one. `portfolio-filter-editor.tsx` stays security-grain and
says so. `currentTicker` in `client/lib/identifiers.ts` reads listing
identifiers.

A listing is disclosed by its currency -- "VOD (GBP)" -- which
docs/spec/display-currency.md already has the vocabulary for, and which tells a
user something a MIC does not.

`client/app/transactions/page.tsx` says which claim its Currency column makes.
The column sits beside the price and reads as the currency that price is in,
which `figureCurrency` answers correctly -- but a posting whose source named no
line reaches it with only a settlement currency, and the cell then shows the
account's currency in a column a user reconciling a statement reads as the
security's. The two are the distinction 0156 drew, and the column currently
collapses it again.

A holding on no line, or on a currency-unknown listing, is unpriced and
unvaluable, so it joins the admin attention surface as a repair. The two are
different questions -- nothing said which line this is, against this security's
currency is unknown -- and the surface should say which.

## Outcome

The scope's currency-unknown listing had already gone: 0157 deleted the row and
made `instrument_listings.currency` NOT NULL, so a holding cannot sit on a line
whose currency is unknown. The second question became the security holding no
line at all, which is the same repair asked one level up, and the surfaces say
that instead.

Ending the flattening was the larger half and was not in the scope. The API
carried every name a security answers to in one list per instrument -- the
deferral `Instrument.identifiers` and `AllIdentifiers` both recorded -- and no
surface could have said which grain a name was at while it did. `identifiers` is
now the security's, a line's are on the line, and a third list carries the ones
nobody could place. `AllIdentifiers` survives for the corporate event fetcher,
which wants every name by choice.

A third answer turned up beside the issue's two: a holding whose security was
never identified has no lines to be on, which is not a claim about lines at all.
The holdings row distinguishes all three; the admin count reports the two that
are about lines, its query requiring an instrument.

`currentTicker` reads a line's identifiers when it is told which line and widens
to any of them when it is not. The widening is what keeps the transaction list
labelled: a posting names a security and the response carries no line, so there
is nothing there to pick with.

Landed in three changes: the grain split at the API with the admin instruments
page; holdings, checkpoints, the transaction column and the filter note; and the
admin count.
