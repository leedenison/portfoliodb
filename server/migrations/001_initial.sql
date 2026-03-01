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
  source       TEXT NOT NULL,
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

-- M02: instruments, instrument_identifiers, plugin config, identification errors, txs.instrument_id.
-- For this milestone datamodels are dropped and recreated from scratch; no backfill.

-- Canonical instruments (security master).
CREATE TABLE instruments (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  asset_class TEXT,
  exchange    TEXT,
  currency    TEXT,
  name        TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Identifiers for an instrument. (identifier_type, value) is unique globally.
-- canonical = false only for broker-description identifiers; canonical = true for standard identifiers (ISIN, CUSIP, etc.).
-- Broker descriptions: identifier_type = broker name ('IBKR', 'SCHB'), value = full instrument_description.
CREATE TABLE instrument_identifiers (
  instrument_id   UUID NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  identifier_type TEXT NOT NULL,
  value           TEXT NOT NULL,
  canonical       BOOLEAN NOT NULL DEFAULT true,
  PRIMARY KEY (instrument_id, identifier_type, value),
  UNIQUE (identifier_type, value)
);

CREATE INDEX idx_instrument_identifiers_lookup ON instrument_identifiers (identifier_type, value);

-- Plugin config: which plugins are enabled, precedence (unique), plugin-specific config.
CREATE TABLE identifier_plugin_config (
  plugin_id   TEXT PRIMARY KEY,
  enabled     BOOLEAN NOT NULL DEFAULT true,
  precedence  INT NOT NULL UNIQUE,
  config      JSONB
);

-- Identification errors for a job (e.g. plugin timeout, broker description only).
CREATE TABLE identification_errors (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  job_id                UUID NOT NULL REFERENCES ingestion_jobs (id) ON DELETE CASCADE,
  row_index             INT NOT NULL,
  instrument_description TEXT NOT NULL,
  message               TEXT NOT NULL
);

CREATE INDEX idx_identification_errors_job_id ON identification_errors (job_id);

-- Link txs to instruments. Every tx has an instrument (plugin-resolved or broker description only).
-- Nullable for migration safety when 002 runs on existing DB; for drop-and-recreate deploy, txs is empty.
ALTER TABLE txs ADD COLUMN instrument_id UUID REFERENCES instruments (id);

CREATE INDEX idx_txs_instrument_id ON txs (instrument_id);
