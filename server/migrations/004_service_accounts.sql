CREATE TABLE service_accounts (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name                TEXT NOT NULL,
  client_secret_hash  TEXT NOT NULL,
  role                TEXT NOT NULL DEFAULT 'service_account'
                      CHECK (role IN ('service_account', 'admin')),
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
