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

A holding on no line, or on a currency-unknown listing, is unpriced and
unvaluable, so it joins the admin attention surface as a repair. The two are
different questions -- nothing said which line this is, against this security's
currency is unknown -- and the surface should say which.
