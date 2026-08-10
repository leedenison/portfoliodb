-- Pre-identified instruments with prices for price-admin and performance tests.
-- Uses AMZN, NVDA, TSLA to avoid overlap with the ingestion test (AAPL, MSFT, GOOGL).

-- Instruments.
INSERT INTO instruments (id, asset_class, currency, name)
VALUES
  ('e2e00000-0000-0000-0000-000000000101', 'STOCK', 'USD', 'Amazon.com Inc.'),
  ('e2e00000-0000-0000-0000-000000000102', 'STOCK', 'USD', 'NVIDIA Corp.'),
  ('e2e00000-0000-0000-0000-000000000103', 'STOCK', 'USD', 'Tesla Inc.')
ON CONFLICT (id) DO NOTHING;

-- Identifiers.
INSERT INTO instrument_identifiers (instrument_id, identifier_type, value, canonical)
VALUES
  ('e2e00000-0000-0000-0000-000000000101', 'ISIN', 'US0231351067', true),
  ('e2e00000-0000-0000-0000-000000000101', 'MIC_TICKER', 'AMZN', true),
  ('e2e00000-0000-0000-0000-000000000102', 'ISIN', 'US67066G1040', true),
  ('e2e00000-0000-0000-0000-000000000102', 'MIC_TICKER', 'NVDA', true),
  ('e2e00000-0000-0000-0000-000000000103', 'ISIN', 'US88160R1014', true),
  ('e2e00000-0000-0000-0000-000000000103', 'MIC_TICKER', 'TSLA', true)
ON CONFLICT DO NOTHING;

-- Transactions referencing the instruments (for holdings/valuation). Every posting
-- belongs to a group, so each trade gets one; the ids are explicit because the
-- postings reference them. A priced TRADE_ASSET converts, so it weighs its
-- consideration in the settlement currency: quantity * unit_price, against an
-- equal and opposite counterparty. The group has to balance -- the constraint is
-- on.
INSERT INTO tx_groups (id, user_id, timestamp)
VALUES
  ('e2e00000-0000-0000-0000-000000000201', 'e2e00000-0000-0000-0000-000000000001', '2024-01-15'),
  ('e2e00000-0000-0000-0000-000000000202', 'e2e00000-0000-0000-0000-000000000001', '2024-01-16'),
  ('e2e00000-0000-0000-0000-000000000203', 'e2e00000-0000-0000-0000-000000000001', '2024-01-17')
ON CONFLICT (id) DO NOTHING;

INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, broker_tx_type, resolved_tx_type, quantity, trading_currency, unit_price, instrument_id, weight, weight_commodity, group_id)
VALUES
  ('e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1', '2024-01-15', 'AMZN - Amazon.com Inc.', ARRAY['TRADE_ASSET'], 'TRADE_ASSET', 8, 'USD', 155.20, 'e2e00000-0000-0000-0000-000000000101', 1241.60, 'cur:USD', 'e2e00000-0000-0000-0000-000000000201'),
  ('e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1', '2024-01-16', 'NVDA - NVIDIA Corp.', ARRAY['TRADE_ASSET'], 'TRADE_ASSET', 15, 'USD', 560.50, 'e2e00000-0000-0000-0000-000000000102', 8407.50, 'cur:USD', 'e2e00000-0000-0000-0000-000000000202'),
  ('e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1', '2024-01-17', 'TSLA - Tesla Inc.', ARRAY['TRADE_ASSET'], 'TRADE_ASSET', 12, 'USD', 218.90, 'e2e00000-0000-0000-0000-000000000103', 2626.80, 'cur:USD', 'e2e00000-0000-0000-0000-000000000203')
ON CONFLICT DO NOTHING;

-- The counterparty that makes each trade balance. EQUITY rather than a USER cash
-- row, because this fixture seeds an opening position rather than a cash trail: an
-- EQUITY leg is value entering the holdings from outside the ledger, and only USER
-- postings reach holdings and valuation, so the counterparty cannot show up as a
-- negative cash balance in the specs that read them.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, broker_tx_type, resolved_tx_type, quantity, trading_currency, settlement_currency, instrument_id, account_type, weight, weight_commodity, group_id)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1', v.at::timestamptz, 'USD', ARRAY['TRADE_ASSET'], 'TRADE_ASSET',
       v.qty, 'USD', 'USD', i.instrument_id, 'EQUITY', v.qty, 'cur:USD', v.group_id::uuid
FROM (VALUES ('2024-01-15', -1241.60, 'e2e00000-0000-0000-0000-000000000201'),
             ('2024-01-16', -8407.50, 'e2e00000-0000-0000-0000-000000000202'),
             ('2024-01-17', -2626.80, 'e2e00000-0000-0000-0000-000000000203')) AS v(at, qty, group_id),
     (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i
ON CONFLICT DO NOTHING;

-- EOD prices: a few days of data for each instrument.
INSERT INTO eod_prices (instrument_id, price_date, open, high, low, close, volume, data_provider)
VALUES
  -- AMZN
  ('e2e00000-0000-0000-0000-000000000101', '2024-01-15', 154.50, 156.00, 153.80, 155.20, 45000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000101', '2024-01-16', 155.20, 157.30, 154.90, 156.80, 42000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000101', '2024-01-17', 156.80, 158.00, 156.00, 157.50, 47000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000101', '2024-01-18', 157.50, 159.00, 157.00, 158.20, 43000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000101', '2024-01-19', 158.20, 160.00, 157.80, 159.50, 41000000, 'e2e-seed'),
  -- NVDA
  ('e2e00000-0000-0000-0000-000000000102', '2024-01-15', 558.00, 562.00, 556.00, 560.50, 35000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000102', '2024-01-16', 560.50, 565.00, 559.00, 563.20, 38000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000102', '2024-01-17', 563.20, 568.00, 562.00, 566.80, 33000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000102', '2024-01-18', 566.80, 570.00, 565.50, 568.50, 36000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000102', '2024-01-19', 568.50, 572.00, 567.00, 571.00, 34000000, 'e2e-seed'),
  -- TSLA
  ('e2e00000-0000-0000-0000-000000000103', '2024-01-15', 217.00, 220.00, 216.50, 218.90, 55000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000103', '2024-01-16', 218.90, 221.50, 218.00, 220.30, 52000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000103', '2024-01-17', 220.30, 223.00, 219.50, 222.00, 58000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000103', '2024-01-18', 222.00, 224.00, 221.00, 223.50, 50000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000103', '2024-01-19', 223.50, 225.50, 222.50, 224.80, 48000000, 'e2e-seed')
ON CONFLICT (instrument_id, price_date) DO NOTHING;
