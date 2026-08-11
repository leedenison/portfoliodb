-- Transactions for the user archive round trip: three balanced groups under one
-- broker, on pre-identified instruments.
--
-- Separate from instruments.sql, which seeds the same shape for the holdings and
-- valuation specs, because its balancing leg carries the trade's own TRADE_ASSET
-- type against a currency instrument. A round trip has to be re-importable to
-- prove anything, so the cash leg here carries TRADE_CASH, as a converter's
-- derived money leg does.
--
-- Pre-identified because the round trip is about the archive rather than about
-- identification: an instrument carrying the identifier the export writes is
-- found in the database on the way back in, so no plugin is called and the suite
-- needs no cassette.

INSERT INTO instruments (id, asset_class, currency, name)
VALUES
  ('e2e00000-0000-0000-0000-000000000401', 'STOCK', 'USD', 'Amazon.com Inc.'),
  ('e2e00000-0000-0000-0000-000000000402', 'STOCK', 'USD', 'NVIDIA Corp.'),
  ('e2e00000-0000-0000-0000-000000000403', 'STOCK', 'USD', 'Tesla Inc.')
ON CONFLICT (id) DO NOTHING;

-- Two identifiers each, so the export has a priority order to apply: MIC_TICKER
-- outranks ISIN, and bestIdentifierJoin is what decides.
INSERT INTO instrument_identifiers (instrument_id, identifier_type, value, canonical)
VALUES
  ('e2e00000-0000-0000-0000-000000000401', 'ISIN', 'US0231351067', true),
  ('e2e00000-0000-0000-0000-000000000401', 'MIC_TICKER', 'AMZN', true),
  ('e2e00000-0000-0000-0000-000000000402', 'ISIN', 'US67066G1040', true),
  ('e2e00000-0000-0000-0000-000000000402', 'MIC_TICKER', 'NVDA', true),
  ('e2e00000-0000-0000-0000-000000000403', 'ISIN', 'US88160R1014', true),
  ('e2e00000-0000-0000-0000-000000000403', 'MIC_TICKER', 'TSLA', true)
ON CONFLICT DO NOTHING;

-- Every posting belongs to a group, and the ids are explicit because the
-- postings reference them.
INSERT INTO tx_groups (id, user_id, timestamp)
VALUES
  ('e2e00000-0000-0000-0000-000000000411', 'e2e00000-0000-0000-0000-000000000001', '2024-01-15'),
  ('e2e00000-0000-0000-0000-000000000412', 'e2e00000-0000-0000-0000-000000000001', '2024-01-16'),
  ('e2e00000-0000-0000-0000-000000000413', 'e2e00000-0000-0000-0000-000000000001', '2024-01-17')
ON CONFLICT (id) DO NOTHING;

-- The trades. A priced TRADE_ASSET converts, so it weighs its consideration in
-- the settlement currency: quantity * unit_price.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description,
                 broker_tx_type, resolved_tx_type, quantity,
                 trading_currency, settlement_currency, unit_price, instrument_id,
                 weight, weight_commodity, group_id)
VALUES
  ('e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1', '2024-01-15', 'AMZN - Amazon.com Inc.', ARRAY['TRADE_ASSET'], 'TRADE_ASSET', 8, 'USD', 'USD', 155.20, 'e2e00000-0000-0000-0000-000000000401', 1241.60, 'cur:USD', 'e2e00000-0000-0000-0000-000000000411'),
  ('e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1', '2024-01-16', 'NVDA - NVIDIA Corp.', ARRAY['TRADE_ASSET'], 'TRADE_ASSET', 15, 'USD', 'USD', 560.50, 'e2e00000-0000-0000-0000-000000000402', 8407.50, 'cur:USD', 'e2e00000-0000-0000-0000-000000000412'),
  ('e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1', '2024-01-17', 'TSLA - Tesla Inc.', ARRAY['TRADE_ASSET'], 'TRADE_ASSET', 12, 'USD', 'USD', 218.90, 'e2e00000-0000-0000-0000-000000000403', 2626.80, 'cur:USD', 'e2e00000-0000-0000-0000-000000000413')
ON CONFLICT DO NOTHING;

-- The money that paid for each trade. EQUITY rather than a USER cash row,
-- because this seeds an opening position rather than a cash trail: only USER
-- postings reach holdings and valuation, so the counterparty cannot show up as a
-- negative cash balance. No unit price, so it weighs its own quantity in its own
-- currency and the group sums to zero.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description,
                 broker_tx_type, resolved_tx_type, quantity,
                 trading_currency, settlement_currency, instrument_id, account_type,
                 weight, weight_commodity, group_id)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1', v.at::timestamptz, 'USD',
       ARRAY['TRADE_CASH'], 'TRADE_CASH',
       v.qty, 'USD', 'USD', i.instrument_id, 'EQUITY', v.qty, 'cur:USD', v.group_id::uuid
FROM (VALUES ('2024-01-15', -1241.60, 'e2e00000-0000-0000-0000-000000000411'),
             ('2024-01-16', -8407.50, 'e2e00000-0000-0000-0000-000000000412'),
             ('2024-01-17', -2626.80, 'e2e00000-0000-0000-0000-000000000413')) AS v(at, qty, group_id),
     (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i
ON CONFLICT DO NOTHING;
