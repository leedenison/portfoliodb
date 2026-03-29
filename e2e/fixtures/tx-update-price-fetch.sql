-- Pre-identified INTC instrument with full price coverage.
-- Uses INTC to avoid overlap with other e2e tests.

-- Instrument.
INSERT INTO instruments (id, asset_class, currency, name)
VALUES ('e2e00000-0000-0000-0000-000000000301', 'STOCK', 'USD', 'Intel Corporation')
ON CONFLICT (id) DO NOTHING;

-- Identifiers.
INSERT INTO instrument_identifiers (instrument_id, identifier_type, value, canonical)
VALUES
  ('e2e00000-0000-0000-0000-000000000301', 'ISIN', 'US4581401001', true),
  ('e2e00000-0000-0000-0000-000000000301', 'MIC_TICKER', 'INTC', true)
ON CONFLICT DO NOTHING;

-- Initial transaction: buy 10 INTC on 2024-01-15.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type, quantity, trading_currency, unit_price, instrument_id)
VALUES
  ('e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1', '2024-01-15', 'INTC - Intel Corp.', 'BUYSTOCK', 10, 'USD', 47.50, 'e2e00000-0000-0000-0000-000000000301')
ON CONFLICT DO NOTHING;

-- Dense price coverage from 2024-01-10 (lookback) through yesterday.
-- This ensures PriceGaps returns empty for the held range.
INSERT INTO eod_prices (instrument_id, price_date, close, data_provider, synthetic)
SELECT
  'e2e00000-0000-0000-0000-000000000301',
  d::date,
  45.00 + (EXTRACT(EPOCH FROM d - '2024-01-10'::date) / 86400) * 0.01,
  'e2e-seed',
  true
FROM generate_series('2024-01-10'::date, CURRENT_DATE, '1 day') d
ON CONFLICT (instrument_id, price_date) DO NOTHING;
