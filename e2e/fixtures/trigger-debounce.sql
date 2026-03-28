-- Fixture for price trigger debounce test.
-- Seeds pre-identified instruments with partial price coverage so that
-- PriceGaps returns non-empty results (triggering a RUNNING transition).
-- Disables price plugins so the cycle exits early without HTTP calls.

-- Instruments (different from ingestion test to avoid cassette overlap).
INSERT INTO instruments (id, asset_class, currency, name)
VALUES
  ('e2e00000-0000-0000-0000-000000000201', 'STOCK', 'USD', 'Debounce Stock A'),
  ('e2e00000-0000-0000-0000-000000000202', 'STOCK', 'USD', 'Debounce Stock B')
ON CONFLICT (id) DO NOTHING;

-- Identifiers.
INSERT INTO instrument_identifiers (instrument_id, identifier_type, value, canonical)
VALUES
  ('e2e00000-0000-0000-0000-000000000201', 'MIC_TICKER', 'DBNA', true),
  ('e2e00000-0000-0000-0000-000000000202', 'MIC_TICKER', 'DBNB', true)
ON CONFLICT DO NOTHING;

-- Transactions for the regular test user.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type, quantity, trading_currency, unit_price, instrument_id)
VALUES
  ('e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1', '2024-06-01', 'DBNA - Debounce Stock A', 'BUYSTOCK', 10, 'USD', 100.00, 'e2e00000-0000-0000-0000-000000000201'),
  ('e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1', '2024-06-01', 'DBNB - Debounce Stock B', 'BUYSTOCK', 5, 'USD', 200.00, 'e2e00000-0000-0000-0000-000000000202')
ON CONFLICT DO NOTHING;

-- Partial prices: only a few days so price gaps exist.
INSERT INTO eod_prices (instrument_id, price_date, open, high, low, close, volume, data_provider)
VALUES
  ('e2e00000-0000-0000-0000-000000000201', '2024-06-01', 100, 101, 99, 100, 1000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000201', '2024-06-02', 100, 102, 99, 101, 1000, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000202', '2024-06-01', 200, 202, 198, 200, 500, 'e2e-seed'),
  ('e2e00000-0000-0000-0000-000000000202', '2024-06-02', 200, 203, 198, 201, 500, 'e2e-seed')
ON CONFLICT (instrument_id, price_date) DO NOTHING;

-- Disable price plugins so the cycle does not make any HTTP calls.
-- The cycle still transitions to RUNNING (because gaps exist) then exits
-- early when it finds no enabled price plugins.
UPDATE plugin_config SET enabled = false WHERE category = 'price';
