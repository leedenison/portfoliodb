-- Residual postings for the admin imbalance report.
--
-- In production these rows are written by ingestion, which routes the residual of
-- an unbalanced group to IMBALANCE and each side of a journal to
-- TRANSFER_CLEARING. Seeding them directly keeps the fixture about the report
-- rather than about the converters.
--
-- Every posting belongs to a group, so each residual gets one. The ids are explicit
-- because the postings reference them. Every residual here is a cash posting with no
-- price, so it weighs its own quantity in the currency it is denominated in.
--
-- Timestamps are relative to now() so the age assertions do not rot.

INSERT INTO tx_groups (id, user_id, timestamp)
VALUES
  ('e2e00000-0000-0000-0000-000000000301', 'e2e00000-0000-0000-0000-000000000001', now() - INTERVAL '5 days'),
  ('e2e00000-0000-0000-0000-000000000302', 'e2e00000-0000-0000-0000-000000000001', now() - INTERVAL '6 days'),
  ('e2e00000-0000-0000-0000-000000000303', 'e2e00000-0000-0000-0000-000000000001', now() - INTERVAL '60 days'),
  ('e2e00000-0000-0000-0000-000000000304', 'e2e00000-0000-0000-0000-000000000001', now() - INTERVAL '59 days'),
  ('e2e00000-0000-0000-0000-000000000305', 'e2e00000-0000-0000-0000-000000000001', now() - INTERVAL '40 days'),
  ('e2e00000-0000-0000-0000-000000000306', 'e2e00000-0000-0000-0000-000000000001', now() - INTERVAL '2 days')
ON CONFLICT (id) DO NOTHING;

-- A dividend the converter reported as cash only, so its income leg is missing and
-- the whole value lands in Imbalance. Negative: value arriving from outside the
-- ledger.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type,
                 quantity, trading_currency, settlement_currency, instrument_id, account_type,
                 weight, weight_commodity, group_id)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1',
       now() - INTERVAL '5 days', 'USD', 'INCOME', -137.08, 'USD', 'USD', i.instrument_id, 'IMBALANCE',
       -137.08, 'cur:USD', 'e2e00000-0000-0000-0000-000000000301'
FROM (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i;

-- A trade whose price and cash total disagree: the commission the broker netted
-- into the total and did not report separately.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type,
                 quantity, trading_currency, settlement_currency, instrument_id, account_type,
                 weight, weight_commodity, group_id)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1',
       now() - INTERVAL '6 days', 'USD', 'BUYSTOCK', 4.95, 'USD', 'USD', i.instrument_id, 'IMBALANCE',
       4.95, 'cur:USD', 'e2e00000-0000-0000-0000-000000000302'
FROM (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i;

-- A journal between two Schwab accounts whose second side did arrive. Both sides
-- are TRANSFER_CLEARING postings, so the report has to consume them against each
-- other; neither should appear. They arrive in separate statements, so they are
-- separate groups -- pairing them is 0068.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type,
                 quantity, trading_currency, settlement_currency, instrument_id, account_type,
                 weight, weight_commodity, group_id)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'SCHB', v.account,
       now() - (v.days || ' days')::INTERVAL, 'USD', 'JRNLFUND', v.qty, 'USD', 'USD',
       i.instrument_id, 'TRANSFER_CLEARING', v.qty, 'cur:USD', v.group_id::uuid
FROM (VALUES ('SCH-1', 1000.00, '60', 'e2e00000-0000-0000-0000-000000000303'),
             ('SCH-2', -1000.00, '59', 'e2e00000-0000-0000-0000-000000000304')) AS v(account, qty, days, group_id),
     (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i;

-- One side of a transfer out of an IBKR account, long enough ago that the other
-- side is not coming.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type,
                 quantity, trading_currency, settlement_currency, instrument_id, account_type,
                 weight, weight_commodity, group_id)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'IBKR', 'U-OLD',
       now() - INTERVAL '40 days', 'USD', 'JRNLFUND', 500.00, 'USD', 'USD', i.instrument_id, 'TRANSFER_CLEARING',
       500.00, 'cur:USD', 'e2e00000-0000-0000-0000-000000000305'
FROM (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i;

-- A transfer imported two days ago whose other side may still be on its way. It
-- must stay quiet.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type,
                 quantity, trading_currency, settlement_currency, instrument_id, account_type,
                 weight, weight_commodity, group_id)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'IBKR', 'U-NEW',
       now() - INTERVAL '2 days', 'USD', 'JRNLFUND', 250.00, 'USD', 'USD', i.instrument_id, 'TRANSFER_CLEARING',
       250.00, 'cur:USD', 'e2e00000-0000-0000-0000-000000000306'
FROM (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i;
