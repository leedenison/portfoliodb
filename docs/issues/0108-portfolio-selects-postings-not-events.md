---
status: open
title: A portfolio selects postings, not the events they are legs of
milestone: M22
---

`portfolio_matched_txs` matches one posting at a time, so a filter can take part
of a transaction group and leave the rest. A portfolio filtered on an instrument
matches a buy's stock leg and not the cash that paid for it; one filtered on an
account matches a leg whose counterparty the server routed to a different one.

That was invisible while the transaction list showed one row per posting. Now
that it shows one row per event, with the other legs behind a disclosure, a
portfolio view can show an event whose expansion is empty or missing the leg
that explains it, and the same event can lead with a different row depending on
which portfolio is selected -- the principal is picked from the legs that
matched.

The list does the simplest thing for now and shows only the matched legs.
Settling this means deciding what a portfolio selects: postings, as today, or
whole groups reachable through a matching posting. Holdings and valuation read
the same view, so the answer is not the transaction list's alone -- valuing a
group's cash leg because its stock leg matched is a different claim about what
the portfolio holds.
