-- E2E seed: base data (users, portfolio).
-- Plugin config is seeded separately by the Makefile (different for record vs replay).

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
