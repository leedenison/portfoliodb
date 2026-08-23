---
status: open
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
