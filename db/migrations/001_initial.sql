-- M01 datamodel: holdings only. No instrument identification, prices or corporate events.
-- Holdings are calculated from transactions at query time, not materialized.

-- Users own portfolios. Auth subject from OAuth ID token; name and email from create-user.
CREATE TABLE users (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  auth_sub   TEXT NOT NULL UNIQUE,
  name       TEXT NOT NULL,
  email      TEXT NOT NULL,
  role       TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_users_auth_sub ON users (auth_sub);

-- Portfolios are user-owned containers for transactions.
CREATE TABLE portfolios (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_portfolios_user_id ON portfolios (user_id);

-- Transactions. Natural key (portfolio_id, broker, timestamp, instrument_description)
-- for idempotent single-tx upsert. Bulk upsert replaces by (portfolio_id, broker, period).
CREATE TABLE txs (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  portfolio_id          UUID NOT NULL REFERENCES portfolios (id) ON DELETE CASCADE,
  broker                TEXT NOT NULL CHECK (broker IN ('IBKR', 'SCHB')),
  timestamp             TIMESTAMPTZ NOT NULL,
  instrument_description TEXT NOT NULL,
  tx_type               TEXT NOT NULL,
  quantity              DOUBLE PRECISION NOT NULL,
  currency              TEXT,
  unit_price            DOUBLE PRECISION,
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (portfolio_id, broker, timestamp, instrument_description)
);

CREATE INDEX idx_txs_portfolio_broker_time ON txs (portfolio_id, broker, timestamp);

-- Async ingestion jobs. status and validation_errors surfaced via front-end API.
CREATE TABLE ingestion_jobs (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  portfolio_id UUID NOT NULL REFERENCES portfolios (id) ON DELETE CASCADE,
  broker       TEXT NOT NULL CHECK (broker IN ('IBKR', 'SCHB')),
  period_from  TIMESTAMPTZ,
  period_to    TIMESTAMPTZ,
  status       TEXT NOT NULL CHECK (status IN ('PENDING', 'RUNNING', 'SUCCESS', 'FAILED')),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_ingestion_jobs_portfolio ON ingestion_jobs (portfolio_id);
CREATE INDEX idx_ingestion_jobs_status ON ingestion_jobs (status);

-- Validation errors for async ingestion. row_index, field, message per API.
CREATE TABLE validation_errors (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  job_id     UUID NOT NULL REFERENCES ingestion_jobs (id) ON DELETE CASCADE,
  row_index  INT NOT NULL,
  field      TEXT NOT NULL,
  message    TEXT NOT NULL
);

CREATE INDEX idx_validation_errors_job_id ON validation_errors (job_id);
