-- Residual postings for the admin imbalance report.
--
-- In production these rows are written by ingestion, which routes the residual of
-- an unbalanced group to IMBALANCE and each side of a journal to
-- TRANSFER_CLEARING. Seeding them directly keeps the fixture about the report
-- rather than about the converters.
--
-- Timestamps are relative to now() so the age assertions do not rot.

-- A dividend the converter reported as cash only, so its income leg is missing and
-- the whole value lands in Imbalance. Negative: value arriving from outside the
-- ledger.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type,
                 quantity, trading_currency, settlement_currency, instrument_id, account_type)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1',
       now() - INTERVAL '5 days', 'USD', 'INCOME', -137.08, 'USD', 'USD', i.instrument_id, 'IMBALANCE'
FROM (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i;

-- A trade whose price and cash total disagree: the commission the broker netted
-- into the total and did not report separately.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type,
                 quantity, trading_currency, settlement_currency, instrument_id, account_type)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'FIDELITY', 'ACC-1',
       now() - INTERVAL '6 days', 'USD', 'BUYSTOCK', 4.95, 'USD', 'USD', i.instrument_id, 'IMBALANCE'
FROM (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i;

-- A journal between two Schwab accounts whose second side did arrive. Both sides
-- are TRANSFER_CLEARING postings, so the report has to consume them against each
-- other; neither should appear.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type,
                 quantity, trading_currency, settlement_currency, instrument_id, account_type)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'SCHB', v.account,
       now() - (v.days || ' days')::INTERVAL, 'USD', 'JRNLFUND', v.qty, 'USD', 'USD',
       i.instrument_id, 'TRANSFER_CLEARING'
FROM (VALUES ('SCH-1', 1000.00, '60'), ('SCH-2', -1000.00, '59')) AS v(account, qty, days),
     (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i;

-- One side of a transfer out of an IBKR account, long enough ago that the other
-- side is not coming.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type,
                 quantity, trading_currency, settlement_currency, instrument_id, account_type)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'IBKR', 'U-OLD',
       now() - INTERVAL '40 days', 'USD', 'JRNLFUND', 500.00, 'USD', 'USD', i.instrument_id, 'TRANSFER_CLEARING'
FROM (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i;

-- A transfer imported two days ago whose other side may still be on its way. It
-- must stay quiet.
INSERT INTO txs (user_id, broker, account, timestamp, instrument_description, tx_type,
                 quantity, trading_currency, settlement_currency, instrument_id, account_type)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'IBKR', 'U-NEW',
       now() - INTERVAL '2 days', 'USD', 'JRNLFUND', 250.00, 'USD', 'USD', i.instrument_id, 'TRANSFER_CLEARING'
FROM (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i;
