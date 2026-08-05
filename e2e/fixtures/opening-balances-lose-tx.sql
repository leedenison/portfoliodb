-- Lose the AMZN buy seeded by instruments.sql, the way a converter that silently
-- drops a row does.
--
-- Deleting the group takes both its postings, which is what ingestion's own delete
-- path does: the group is the unit of deletion, so no path can leave a counterparty
-- behind without the posting it balances.
--
-- Nothing else is touched. The declarations still say what the user said, and no
-- recompute is triggered -- which is the point: a checked declaration is measured on
-- read, so the next read of it disagrees without anything having invalidated it.

DELETE FROM tx_groups
WHERE id = 'e2e00000-0000-0000-0000-000000000201';
