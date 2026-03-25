-- E2E seed: replay mode.
-- Dummy API keys, no rate limits (calls_per_min omitted = unlimited).
-- Run after server starts and applies migrations.

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

-- Plugin config: enable cash plugins, disable external plugins.
-- Description plugins.
INSERT INTO plugin_config (plugin_id, category, enabled, precedence, config)
VALUES
  ('openai', 'description', true, 1,
   '{"openai_api_key":"REDACTED","openai_model":"gpt-4o-mini","openai_base_url":"https://api.openai.com"}'::jsonb),
  ('cash', 'description', true, 2, '{}'::jsonb)
ON CONFLICT (plugin_id, category) DO UPDATE SET
  enabled = EXCLUDED.enabled,
  precedence = EXCLUDED.precedence,
  config = EXCLUDED.config;

-- Identifier plugins.
INSERT INTO plugin_config (plugin_id, category, enabled, precedence, config)
VALUES
  ('openfigi', 'identifier', true, 1,
   '{"openfigi_api_key":"REDACTED","openfigi_base_url":"https://api.openfigi.com"}'::jsonb),
  ('cash', 'identifier', true, 2, '{}'::jsonb),
  ('eodhd', 'identifier', true, 3,
   '{"eodhd_api_key":"REDACTED","eodhd_base_url":"https://eodhd.com"}'::jsonb),
  ('massive', 'identifier', true, 4,
   '{"massive_api_key":"REDACTED","massive_base_url":"https://api.massive.com"}'::jsonb)
ON CONFLICT (plugin_id, category) DO UPDATE SET
  enabled = EXCLUDED.enabled,
  precedence = EXCLUDED.precedence,
  config = EXCLUDED.config;

-- Price plugins.
INSERT INTO plugin_config (plugin_id, category, enabled, precedence, config)
VALUES
  ('massive', 'price', true, 1,
   '{"massive_api_key":"REDACTED","massive_base_url":"https://api.massive.com"}'::jsonb)
ON CONFLICT (plugin_id, category) DO UPDATE SET
  enabled = EXCLUDED.enabled,
  precedence = EXCLUDED.precedence,
  config = EXCLUDED.config;
