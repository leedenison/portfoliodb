# Portfolio views

Portfolios are **views** over transaction sets. Transactions are owned by the user and associated with a broker and account; they are not owned by a portfolio. A portfolio defines which transactions are visible in that view via **filters**.

## Model

- **Portfolio** (table `portfolios`): `id`, `user_id`, `name`, `created_at`. No direct FK from transactions.
- **Portfolio filters** (table `portfolio_filters`): `portfolio_id`, `filter_type`, `filter_value`.
  - `filter_type` is one of: `broker`, `account`, `instrument`.
  - `filter_value` is text: broker name (e.g. `IBKR`), account string, or instrument UUID.
  - One row per filter; a portfolio may have multiple filters.

## View semantics

"Transactions in portfolio P" = all transactions where:

1. `tx.user_id = portfolio.user_id`, and
2. At least one filter exists for P (zero filters = empty portfolio), and
3. For each filter category that has filters, the transaction matches at least one filter in that category:
   - `broker`: `tx.broker` matches any broker filter value
   - `account`: `tx.account` matches any account filter value
   - `instrument`: `tx.instrument_id` matches any instrument filter value

Categories are **ANDed**: a transaction must satisfy every category that has filters. Within each category, filters are **ORed**: matching any one value in the category is sufficient. A category with no filter rows is unconstrained (all transactions pass that category).

No `DISTINCT` is needed -- the view joins portfolios to txs without multiplying rows through filter rows.

## APIs (Stage 3)

- **ListTxs(portfolio_id)** returns transactions in that portfolio (AND-between-categories, user-scoped).
- **GetHoldings(portfolio_id, as_of)** returns holdings computed from that transaction set.
- **ListBrokersAndAccounts()** returns distinct broker/account pairs for the authenticated user, grouped by broker. Used by the filter editing UI to populate broker and account checkboxes.
- Portfolio CRUD is extended with list/create/update/delete of filter rows (or a single "filters" structure in Create/Update).

## Future

- Shared portfolios: users may contribute read-only views on transaction sets to other users.
- Multiple filter rows per portfolio allow views like "all my IBKR txs in account IRA for instrument AAPL".
