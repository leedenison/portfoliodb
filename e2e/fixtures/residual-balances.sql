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
-- Each residual gets an equal and opposite EQUITY counterparty, because the balance
-- constraint is on and a residual alone in its group does not sum to zero. In
-- production that counterparty is the posting the residual was routed against;
-- EQUITY keeps it out of holdings and valuation, which this fixture is not about.
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
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description,
                 broker_tx_type, resolved_tx_type,
                 quantity, trading_currency, settlement_currency, instrument_id, account_type,
                 weight, weight_commodity, group_id)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1',
       now() - INTERVAL '5 days', 'USD', ARRAY['INCOME'], 'INCOME', -137.08, 'USD', 'USD', i.instrument_id, 'IMBALANCE',
       -137.08, 'cur:USD', 'e2e00000-0000-0000-0000-000000000301'
FROM (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i;

-- A trade whose price and cash total disagree: the commission the broker netted
-- into the total and did not report separately.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description,
                 broker_tx_type, resolved_tx_type,
                 quantity, trading_currency, settlement_currency, instrument_id, account_type,
                 weight, weight_commodity, group_id)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1',
       now() - INTERVAL '6 days', 'USD', ARRAY['TRADE_ASSET'], 'TRADE_ASSET', 4.95, 'USD', 'USD', i.instrument_id, 'IMBALANCE',
       4.95, 'cur:USD', 'e2e00000-0000-0000-0000-000000000302'
FROM (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i;

-- A journal between two Schwab accounts whose second side did arrive. Both sides
-- are TRANSFER_CLEARING postings in separate groups, because they arrived in
-- separate statements; the transfer_matches row at the bottom of this file pairs
-- them, and neither should appear in the report.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description,
                 broker_tx_type, resolved_tx_type,
                 quantity, trading_currency, settlement_currency, instrument_id, account_type,
                 weight, weight_commodity, group_id)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'SCHB', v.account,
       now() - (v.days || ' days')::INTERVAL, 'USD', ARRAY['TRANSFER'], 'TRANSFER', v.qty, 'USD', 'USD',
       i.instrument_id, 'TRANSFER_CLEARING', v.qty, 'cur:USD', v.group_id::uuid
FROM (VALUES ('SCH-1', 1000.00, '60', 'e2e00000-0000-0000-0000-000000000303'),
             ('SCH-2', -1000.00, '59', 'e2e00000-0000-0000-0000-000000000304')) AS v(account, qty, days, group_id),
     (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i;

-- One side of a transfer out of an IBKR account, long enough ago that the other
-- side is not coming.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description,
                 broker_tx_type, resolved_tx_type,
                 quantity, trading_currency, settlement_currency, instrument_id, account_type,
                 weight, weight_commodity, group_id)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'IBKR', 'U-OLD',
       now() - INTERVAL '40 days', 'USD', ARRAY['TRANSFER'], 'TRANSFER', 500.00, 'USD', 'USD', i.instrument_id, 'TRANSFER_CLEARING',
       500.00, 'cur:USD', 'e2e00000-0000-0000-0000-000000000305'
FROM (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i;

-- A transfer imported two days ago whose other side may still be on its way. It
-- must stay quiet.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description,
                 broker_tx_type, resolved_tx_type,
                 quantity, trading_currency, settlement_currency, instrument_id, account_type,
                 weight, weight_commodity, group_id)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'IBKR', 'U-NEW',
       now() - INTERVAL '2 days', 'USD', ARRAY['TRANSFER'], 'TRANSFER', 250.00, 'USD', 'USD', i.instrument_id, 'TRANSFER_CLEARING',
       250.00, 'cur:USD', 'e2e00000-0000-0000-0000-000000000306'
FROM (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i;

-- The counterparties. One per group, equal and opposite, so every group above sums
-- to zero in cur:USD. The report reads only the residual account types, so these are
-- invisible to it.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description,
                 broker_tx_type, resolved_tx_type,
                 quantity, trading_currency, settlement_currency, instrument_id, account_type,
                 weight, weight_commodity, group_id)
SELECT 'e2e00000-0000-0000-0000-000000000001', v.broker, v.account,
       now() - (v.days || ' days')::INTERVAL, 'USD', ARRAY[v.tx_type], v.tx_type, v.qty, 'USD', 'USD',
       i.instrument_id, 'EQUITY', v.qty, 'cur:USD', v.group_id::uuid
FROM (VALUES ('FIDELITY', 'ACC-1', '5',  'INCOME',      137.08,   'e2e00000-0000-0000-0000-000000000301'),
             ('FIDELITY', 'ACC-1', '6',  'TRADE_ASSET', -4.95,    'e2e00000-0000-0000-0000-000000000302'),
             ('SCHB',     'SCH-1', '60', 'TRANSFER',    -1000.00, 'e2e00000-0000-0000-0000-000000000303'),
             ('SCHB',     'SCH-2', '59', 'TRANSFER',    1000.00,  'e2e00000-0000-0000-0000-000000000304'),
             ('IBKR',     'U-OLD', '40', 'TRANSFER',    -500.00,  'e2e00000-0000-0000-0000-000000000305'),
             ('IBKR',     'U-NEW', '2',  'TRANSFER',    -250.00,  'e2e00000-0000-0000-0000-000000000306'))
     AS v(broker, account, days, tx_type, qty, group_id),
     (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i;

-- The pairing for the settled Schwab journal. SCH-1's residual is positive, so that
-- is the side the value left. With this row both sides are settled and drop out of
-- the report, leaving only the two IBKR sides whose counterparts never arrived.
INSERT INTO transfer_matches (user_id, from_group_id, to_group_id, instrument_id, method)
SELECT 'e2e00000-0000-0000-0000-000000000001',
       'e2e00000-0000-0000-0000-000000000303', 'e2e00000-0000-0000-0000-000000000304',
       i.instrument_id, 'REFERENCE'
FROM (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i;
