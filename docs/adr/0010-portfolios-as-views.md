# Portfolios are filter views, not transaction containers

Transactions are owned by the user and associated with a broker and account; a
portfolio is a **view** over that transaction set defined by filter rows, not a
container that owns transactions. There is no FK from a transaction to a
portfolio. Modelling portfolios as views means the same transaction can appear in
any number of portfolios (including the default "All Holdings"), a transaction
never needs to be copied or re-pointed, and future sharing can grant a read-only
view without moving data.

Filter semantics are ANDed between categories (broker, account, instrument) and
ORed within a category, so a portfolio expresses selections like "any of these
brokers, in any of these accounts". The view joins portfolios to transactions
without multiplying rows through filter rows, so no `DISTINCT` is required.
