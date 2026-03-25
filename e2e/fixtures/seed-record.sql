-- E2E seed: record mode.
-- Real API keys from env vars, real rate limits.
-- Run with: psql -U portfoliodb -d portfoliodb < seed-record.sql
-- Requires env vars: OPENFIGI_API_KEY, MASSIVE_API_KEY, EODHD_API_KEY, OPENAI_API_KEY

\set openfigi_key `echo $OPENFIGI_API_KEY`
\set massive_key `echo $MASSIVE_API_KEY`
\set eodhd_key `echo $EODHD_API_KEY`
\set openai_key `echo $OPENAI_API_KEY`

-- Test user (regular).
INSERT INTO users (id, auth_sub, name, email, role)
VALUES (
  'e2e00000-0000-0000-0000-000000000001',
  'e2e-test-sub-001',
  'E2E Test User',
  'e2e@test.example.com',
  'user'
)
ON CONFLICT (auth_sub) DO UPDATE SET
  name = EXCLUDED.name,
  email = EXCLUDED.email,
  role = EXCLUDED.role;

-- Test admin user.
INSERT INTO users (id, auth_sub, name, email, role)
VALUES (
  'e2e00000-0000-0000-0000-000000000002',
  'e2e-admin-sub-001',
  'E2E Admin User',
  'e2e-admin@test.example.com',
  'admin'
)
ON CONFLICT (auth_sub) DO UPDATE SET
  name = EXCLUDED.name,
  email = EXCLUDED.email,
  role = EXCLUDED.role;

-- Default portfolio for test user.
INSERT INTO portfolios (id, user_id, name)
VALUES (
  'e2e00000-0000-0000-0000-000000000010',
  'e2e00000-0000-0000-0000-000000000001',
  'E2E Test Portfolio'
)
ON CONFLICT (id) DO NOTHING;

-- Plugin config with real keys and rate limits for recording.
-- Description plugins.
INSERT INTO plugin_config (plugin_id, category, enabled, precedence, config)
VALUES
  ('openai', 'description', true, 1,
   jsonb_build_object(
     'openai_api_key', :'openai_key',
     'openai_model', 'gpt-4o-mini',
     'openai_base_url', 'https://api.openai.com')),
  ('cash', 'description', true, 2, '{}'::jsonb)
ON CONFLICT (plugin_id, category) DO UPDATE SET
  enabled = EXCLUDED.enabled,
  precedence = EXCLUDED.precedence,
  config = EXCLUDED.config;

-- Identifier plugins.
INSERT INTO plugin_config (plugin_id, category, enabled, precedence, config)
VALUES
  ('openfigi', 'identifier', true, 1,
   jsonb_build_object(
     'openfigi_api_key', :'openfigi_key',
     'openfigi_base_url', 'https://api.openfigi.com')),
  ('cash', 'identifier', true, 2, '{}'::jsonb),
  ('eodhd', 'identifier', true, 3,
   jsonb_build_object(
     'eodhd_api_key', :'eodhd_key',
     'eodhd_base_url', 'https://eodhd.com',
     'eodhd_calls_per_min', 20)),
  ('massive', 'identifier', true, 4,
   jsonb_build_object(
     'massive_api_key', :'massive_key',
     'massive_base_url', 'https://api.massive.com',
     'massive_calls_per_min', 5))
ON CONFLICT (plugin_id, category) DO UPDATE SET
  enabled = EXCLUDED.enabled,
  precedence = EXCLUDED.precedence,
  config = EXCLUDED.config;

-- Price plugins.
INSERT INTO plugin_config (plugin_id, category, enabled, precedence, config)
VALUES
  ('massive', 'price', true, 1,
   jsonb_build_object(
     'massive_api_key', :'massive_key',
     'massive_base_url', 'https://api.massive.com',
     'massive_calls_per_min', 5))
ON CONFLICT (plugin_id, category) DO UPDATE SET
  enabled = EXCLUDED.enabled,
  precedence = EXCLUDED.precedence,
  config = EXCLUDED.config;
