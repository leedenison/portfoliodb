-- One balanced transfer group whose two legs sit on different days, for the
-- period-scoped export and the partial replace it makes reachable.
--
-- This is the shape the Fidelity deposit-run pass produces: it groups on
-- reference proximity rather than the date bucket, because a run in the sample
-- export settles across two days. Any period boundary between the two legs
-- therefore cuts the group in half.
--
-- Raw SQL rather than an upload, for the same reason as user-archive-txs.sql:
-- seeding through ingestion would mean identifying the instruments, which is a
-- paid lookup and a cassette this suite has no other need of. The legs weigh in
-- the seeded USD currency instrument, so a residual routed against them resolves
-- without a plugin.

INSERT INTO tx_groups (id, user_id, timestamp)
VALUES ('e2e00000-0000-0000-0000-000000000421', 'e2e00000-0000-0000-0000-000000000001', '2024-03-10')
ON CONFLICT (id) DO NOTHING;

-- Money leaving one account on the tenth and arriving in another on the
-- eleventh. TRANSFER on both legs -- a cash journal, which is what a deposit run
-- is -- so a residual routed for either half is classed as transfer clearing
-- rather than as an imbalance and the surviving half stays visible to the
-- transfer matcher.
INSERT INTO txs (user_id, broker, account, order_date, trade_date, instrument_description, broker_tx_type, resolved_tx_type, quantity,
                 trading_currency, settlement_currency, instrument_id,
                 weight, weight_commodity, group_id)
SELECT 'e2e00000-0000-0000-0000-000000000001', 'FIDELITY', v.account, v.at::timestamptz, v.at::timestamptz, 'USD', ARRAY['TRANSFER'], 'TRANSFER',
       v.qty, 'USD', 'USD', i.instrument_id, v.qty, 'cur:USD',
       'e2e00000-0000-0000-0000-000000000421'
FROM (VALUES ('2024-03-10', -5000.00, 'ACC-1'),
             ('2024-03-11',  5000.00, 'ACC-2')) AS v(at, qty, account),
     (SELECT instrument_id FROM instrument_identifiers
      WHERE identifier_type = 'CURRENCY' AND value = 'USD' LIMIT 1) i
ON CONFLICT DO NOTHING;

-- The reference both legs of the journal were read from. File-scoped, as a
-- converter emits one: it is comparable within the upload that supplied it and
-- says nothing across two, which is why a leg re-imported on its own does not
-- rejoin the leg that stayed. What repairs that is the grouping cycle, over
-- evidence that reaches further than one file.
INSERT INTO tx_correlations (tx_id, ordinality, label, token, scope, matches)
SELECT t.id, 0, '', 'REC-421', 'FILE', ARRAY['EXACT']
FROM txs t
WHERE t.user_id = 'e2e00000-0000-0000-0000-000000000001'
  AND t.group_id = 'e2e00000-0000-0000-0000-000000000421'
ON CONFLICT DO NOTHING;
