# Half-open date intervals named `[from, before)`

Every date interval in PortfolioDB -- wire API, database columns, and Go and
TypeScript internals -- is half-open `[from, before)` with midnight-UTC bounds,
and the exclusive bound is always named `before` (`date_before`, `period_before`,
`covered_before`, `Before`). Half-open matches PostgreSQL's `daterange` default
and lets abutting intervals compose without arithmetic: the end of one is the
start of the next.

The `before` suffix, rather than a documented `to`, is the decision here. A
comment is only read when someone goes looking, whereas a name is read at every
call site, and renaming turns any call site whose meaning changed into a compile
error rather than a silent off-by-one at a boundary date.

## Consequences

Closed intervals survive only where an external system demands one: the price and
corporate-event provider plugins, the GOOGLEFINANCE formula builder, and the
broker URL templates in the extension. Each converts on the last line before the
outbound call and carries a comment naming the provider's convention.

Human-facing UI keeps inclusive date pickers. The exclusive bound exists so
machine intervals compose, not so users do arithmetic; the client converts once,
at the request boundary.
