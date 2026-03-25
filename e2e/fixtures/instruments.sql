-- Pre-identified instruments with prices for E2E price/admin/performance tests.
-- Run after seed.sql. Depends on test user and portfolio from seed.sql.

-- Instruments.
INSERT INTO instruments (id, asset_class, currency, name)
VALUES
  ('e2e00000-0000-0000-0000-000000000101', 'STOCK', 'USD', 'Apple Inc.'),
  ('e2e00000-0000-0000-0000-000000000102', 'STOCK', 'USD', 'Microsoft Corp.'),
  ('e2e00000-0000-0000-0000-000000000103', 'STOCK', 'USD', 'Alphabet Inc.')
ON CONFLICT (id) DO NOTHING;

-- Identifiers.
INSERT INTO instrument_identifiers (instrument_id, identifier_type, value, canonical)
VALUES
  ('e2e00000-0000-0000-0000-000000000101', 'ISIN', 'US0378331005', true),
  ('e2e00000-0000-0000-0000-000000000101', 'TICKER', 'AAPL', true),
  ('e2e00000-0000-0000-0000-000000000102', 'ISIN', 'US5949181045', true),
  ('e2e00000-0000-0000-0000-000000000102', 'TICKER', 'MSFT', true),
  ('e2e00000-0000-0000-0000-000000000103', 'ISIN', 'US02079K3059', true),
  ('e2e00000-0000-0000-0000-000000000103', 'TICKER', 'GOOGL', true)
ON CONFLICT DO NOTHING;

-- Transactions referencing the instruments (for holdings/valuation).
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type, quantity, trading_currency, unit_price, instrument_id)
VALUES
  ('e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1', '2024-01-15', 'AAPL - Apple Inc.', 'BUYSTOCK', 10, 'USD', 185.50, 'e2e00000-0000-0000-0000-000000000101'),
  ('e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1', '2024-01-16', 'MSFT - Microsoft Corp.', 'BUYSTOCK', 5, 'USD', 398.20, 'e2e00000-0000-0000-0000-000000000102'),
  ('e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1', '2024-01-17', 'GOOGL - Alphabet Inc.', 'BUYSTOCK', 20, 'USD', 141.80, 'e2e00000-0000-0000-0000-000000000103')
ON CONFLICT DO NOTHING;

-- EOD prices: a few days of data for each instrument.
INSERT INTO eod_prices (instrument_id, price_date, open, high, low, close, volume, data_provider)
VALUES
  -- AAPL
  ('e2e00000-0000-0000-0000-000000000101', '2024-01-15', 185.00, 186.50, 184.00, 185.50, 50000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000101', '2024-01-16', 185.50, 187.00, 185.00, 186.20, 48000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000101', '2024-01-17', 186.20, 188.00, 186.00, 187.50, 52000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000101', '2024-01-18', 187.50, 189.00, 187.00, 188.10, 47000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000101', '2024-01-19', 188.10, 189.50, 187.50, 189.00, 45000000, 'e2e-seed'),
  -- MSFT
  ('e2e00000-0000-0000-0000-000000000102', '2024-01-15', 397.00, 399.00, 396.00, 398.20, 30000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000102', '2024-01-16', 398.20, 400.50, 397.50, 399.80, 32000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000102', '2024-01-17', 399.80, 402.00, 399.00, 401.10, 28000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000102', '2024-01-18', 401.10, 403.00, 400.50, 402.50, 31000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000102', '2024-01-19', 402.50, 404.00, 401.00, 403.20, 29000000, 'e2e-seed'),
  -- GOOGL
  ('e2e00000-0000-0000-0000-000000000103', '2024-01-15', 141.00, 142.50, 140.50, 141.80, 25000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000103', '2024-01-16', 141.80, 143.00, 141.00, 142.50, 27000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000103', '2024-01-17', 142.50, 144.00, 142.00, 143.20, 24000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000103', '2024-01-18', 143.20, 145.00, 143.00, 144.00, 26000000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000103', '2024-01-19', 144.00, 145.50, 143.50, 145.10, 23000000, 'e2e-seed')
ON CONFLICT (instrument_id, price_date) DO NOTHING;
