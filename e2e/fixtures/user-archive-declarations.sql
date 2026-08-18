-- Holding declarations for the user archive round trip: one statement covering
-- two holdings, and a second statement at a later date for one of them.
--
-- Layered on user-archive-txs.sql, which seeds the instruments and the
-- transactions. A declaration needs both: it is padded from and checked against
-- the transactions, and the portfolio start date those transactions set is what
-- an as_of_date has to fall on or after.
--
-- The dates sit after the seeded trades (2024-01-15 to 2024-01-17) so nothing is
-- pruned, and the second statement makes the export cut two statements from one
-- account rather than one.
--
-- share_count_basis is left to the table's trigger on the first two rows, which
-- defaults it to as_of_date and is what an absent basis in the file means. The
-- third states one, so the export has a row that has to write it out.

INSERT INTO holding_declarations (user_id, broker, account, instrument_id, declared_qty, as_of_date)
VALUES
  ('e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1', 'e2e00000-0000-0000-0000-000000000401', 8, '2024-01-31'),
  ('e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1', 'e2e00000-0000-0000-0000-000000000402', 15, '2024-01-31')
ON CONFLICT DO NOTHING;

INSERT INTO holding_declarations (user_id, broker, account, instrument_id, declared_qty, as_of_date, share_count_basis)
VALUES
  ('e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1', 'e2e00000-0000-0000-0000-000000000401', 8, '2024-02-29', '2024-03-31')
ON CONFLICT DO NOTHING;
