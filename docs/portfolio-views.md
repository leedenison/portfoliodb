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
2. The transaction matches **any** row in `portfolio_filters` for P:
   - `filter_type = 'broker'`: `tx.broker = filter_value`
   - `filter_type = 'account'`: `tx.account = filter_value`
   - `filter_type = 'instrument'`: `tx.instrument_id = filter_value`

Filters are **ORed**: a transaction is included if it matches any one filter. A transaction must not be counted more than once when listing transactions or computing holdings (deduplicate by `tx.id`).

## APIs (Stage 3)

- **ListTxs(portfolio_id)** returns transactions in that portfolio (OR of filters, user-scoped, deduped).
- **GetHoldings(portfolio_id, as_of)** returns holdings computed from that transaction set.
- Portfolio CRUD is extended with list/create/update/delete of filter rows (or a single "filters" structure in Create/Update).

## Future

- Shared portfolios: users may contribute read-only views on transaction sets to other users.
- Multiple filter rows per portfolio allow views like "all my IBKR txs OR account IRA OR instrument AAPL".
