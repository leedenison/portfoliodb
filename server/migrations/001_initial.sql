-- M01 datamodel: holdings only. No instrument identification, prices or corporate events.
-- Holdings are calculated from transactions at query time, not materialized.

-- Users own portfolios. auth_sub stores Google ID token sub; name and email from token at Auth.
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

-- Portfolio filters: a portfolio is a view over txs matching any of its filters (OR). filter_value is text (broker name, account string, or instrument UUID).
CREATE TABLE portfolio_filters (
  portfolio_id  UUID NOT NULL REFERENCES portfolios (id) ON DELETE CASCADE,
  filter_type   TEXT NOT NULL CHECK (filter_type IN ('broker', 'account', 'instrument')),
  filter_value  TEXT NOT NULL,
  PRIMARY KEY (portfolio_id, filter_type, filter_value)
);

CREATE INDEX idx_portfolio_filters_portfolio ON portfolio_filters (portfolio_id);

-- Transactions. No natural key (broker statements often supply date only). Bulk idempotency
-- by replace-by-period (user_id, broker, period). Single-tx ingestion is append-only.
CREATE TABLE txs (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id               UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  broker                TEXT NOT NULL,
  account               TEXT NOT NULL,
  timestamp             TIMESTAMPTZ NOT NULL,
  instrument_description TEXT NOT NULL,
  tx_type               TEXT NOT NULL,
  quantity              DOUBLE PRECISION NOT NULL,
  currency              TEXT,
  unit_price            DOUBLE PRECISION,
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_txs_user_broker_time ON txs (user_id, broker, timestamp);

-- Async ingestion jobs. status and validation_errors surfaced via front-end API.
CREATE TABLE ingestion_jobs (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  broker       TEXT NOT NULL,
  source       TEXT NOT NULL,
  period_from  TIMESTAMPTZ,
  period_to    TIMESTAMPTZ,
  status       TEXT NOT NULL CHECK (status IN ('PENDING', 'RUNNING', 'SUCCESS', 'FAILED')),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_ingestion_jobs_user ON ingestion_jobs (user_id);
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

-- Canonical instruments (security master).
-- asset_class: controlled vocabulary. OPTION and FUTURE require underlying_id.
CREATE TABLE instruments (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  asset_class  TEXT CHECK (asset_class IS NULL OR asset_class IN ('EQUITY','ETF','MF','CASH','FIXED_INCOME','OPTION','FUTURE')),
  exchange     TEXT,
  currency     TEXT,
  name         TEXT,
  underlying_id UUID REFERENCES instruments (id),
  valid_from   DATE,
  valid_to     DATE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT chk_underlying_required CHECK (
    (asset_class IN ('OPTION','FUTURE') AND underlying_id IS NOT NULL)
    OR (asset_class IS NULL OR asset_class NOT IN ('OPTION','FUTURE'))
  )
);

CREATE INDEX idx_instruments_underlying_id ON instruments (underlying_id);

-- Identifiers for an instrument. (identifier_type, domain, value) is unique globally.
-- domain is NULL for broker-description and for identifiers that have no domain (e.g. ISIN, CUSIP).
-- canonical = false only for broker-description identifiers; canonical = true for standard identifiers (ISIN, CUSIP, etc.).
-- Broker descriptions: identifier_type = source (e.g. 'IBKR:<client>:statement'), domain = NULL, value = full instrument_description.
-- Surrogate PK so domain can be NULL (PostgreSQL PK columns are NOT NULL).
CREATE TABLE instrument_identifiers (
  id              UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
  instrument_id   UUID NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  identifier_type TEXT NOT NULL,
  domain          TEXT,
  value           TEXT NOT NULL,
  canonical       BOOLEAN NOT NULL DEFAULT true
);

-- Per-instrument uniqueness: one row per (instrument_id, identifier_type, domain, value).
CREATE UNIQUE INDEX idx_instrument_identifiers_inst_unique_null_domain ON instrument_identifiers (instrument_id, identifier_type, value) WHERE domain IS NULL;
CREATE UNIQUE INDEX idx_instrument_identifiers_inst_unique_non_null_domain ON instrument_identifiers (instrument_id, identifier_type, domain, value) WHERE domain IS NOT NULL;
-- Global uniqueness: (identifier_type, domain, value) unique across the table.
CREATE UNIQUE INDEX idx_instrument_identifiers_unique_null_domain ON instrument_identifiers (identifier_type, value) WHERE domain IS NULL;
CREATE UNIQUE INDEX idx_instrument_identifiers_unique_non_null_domain ON instrument_identifiers (identifier_type, domain, value) WHERE domain IS NOT NULL;
CREATE INDEX idx_instrument_identifiers_lookup ON instrument_identifiers (identifier_type, COALESCE(domain, ''), value);

-- Plugin config: which plugins are enabled, precedence (unique), plugin-specific config.
CREATE TABLE identifier_plugin_config (
  plugin_id   TEXT PRIMARY KEY,
  enabled     BOOLEAN NOT NULL DEFAULT true,
  precedence  INT NOT NULL UNIQUE,
  config      JSONB
);

-- Description plugin config: plugins that extract identifier hints from broker descriptions.
CREATE TABLE description_plugin_config (
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
ALTER TABLE txs ADD COLUMN instrument_id UUID REFERENCES instruments (id);

CREATE INDEX idx_txs_instrument_id ON txs (instrument_id);
