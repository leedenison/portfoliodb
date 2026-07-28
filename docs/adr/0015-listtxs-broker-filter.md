# Direct broker filter and sort direction on ListTxs

ListTxs accepts an optional `broker` filter and a `descending` sort direction,
alongside the existing portfolio-based filtering. This is a deliberate exception to
the rule that transaction filtering is expressed through portfolio views (see
adr/0010-portfolios-as-views.md): a portfolio is a user-authored, user-visible
concept, and requiring a machine client to have one hand-configured before it can
work makes the client's correctness depend on the user not deleting a view. The
broker filter is not a second way to model a portfolio; it is a query parameter on
a listing that already has period and paging parameters.

The motivating query is "what is the most recent transaction I hold for this
broker", which an automated importer needs to compute its fetch window. Without a
filter and a descending sort that is O(all transactions) in round trips and grows
with history forever; with them it is a single request for a single row. The
database layer already accepted a broker argument that the handler was passing as
nil, so the change is largely a matter of plumbing an existing capability out to the
API.

Adding a sort direction required giving the ordering a deterministic tiebreaker.
Transactions are ordered by timestamp, timestamps are not unique because broker
statements frequently supply only a date, and paging is offset-based -- so rows tied
on the same day could already be skipped or repeated across a page boundary. That
latent bug becomes much easier to hit once a caller can reverse the order, so the
primary key is now part of the ORDER BY in both directions.
