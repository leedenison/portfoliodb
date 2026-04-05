-- Enable TimescaleDB for time-series price data.
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- M01 datamodel: holdings only. No instrument identification, prices or corporate events.
-- Holdings are calculated from transactions at query time, not materialized.

-- Users own portfolios. auth_sub stores Google ID token sub; name and email from token at Auth.
CREATE TABLE users (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  auth_sub   TEXT NOT NULL UNIQUE,
  name       TEXT NOT NULL,
  email      TEXT NOT NULL,
  role       TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
  display_currency TEXT NOT NULL DEFAULT 'USD',
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
  trading_currency      TEXT,
  settlement_currency   TEXT,
  unit_price            DOUBLE PRECISION,
  synthetic_purpose     TEXT CHECK (synthetic_purpose IS NULL OR synthetic_purpose = 'INITIALIZE'),
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_txs_user_broker_time ON txs (user_id, broker, timestamp);

-- Async ingestion jobs. status and validation_errors surfaced via front-end API.
-- job_type distinguishes tx uploads from price imports; broker/source are tx-specific.
-- payload stores the serialized protobuf request and is cleared after processing.
CREATE TABLE ingestion_jobs (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  job_type     TEXT NOT NULL DEFAULT 'tx' CHECK (job_type IN ('tx', 'price')),
  broker       TEXT,
  source       TEXT,
  filename     TEXT,
  period_from  TIMESTAMPTZ,
  period_to    TIMESTAMPTZ,
  status       TEXT NOT NULL CHECK (status IN ('PENDING', 'RUNNING', 'SUCCESS', 'FAILED')),
  total_count      INT NOT NULL DEFAULT 0,
  processed_count  INT NOT NULL DEFAULT 0,
  payload      BYTEA,
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

-- ISO 10383 MIC (Market Identifier Code) reference data.
-- operating_mic FK is DEFERRABLE because operating MICs self-reference (operating_mic = mic)
-- and segment MICs reference their parent; all rows are inserted in a single transaction.
CREATE TABLE exchanges (
  mic           TEXT PRIMARY KEY,
  country       TEXT NOT NULL,
  country_code  TEXT NOT NULL,
  operating_mic TEXT NOT NULL REFERENCES exchanges(mic) DEFERRABLE INITIALLY DEFERRED,
  mic_type      TEXT NOT NULL CHECK (mic_type IN ('O', 'S')),
  name          TEXT NOT NULL,
  acronym       TEXT,
  city          TEXT
);

-- Canonical instruments (security master).
-- asset_class: controlled vocabulary. OPTION and FUTURE require underlying_id.
-- name: denormalized display name, computed by trigger from identifier priority:
--   MIC_TICKER > OPENFIGI_TICKER > OCC > BROKER_DESCRIPTION > CURRENCY > FX_PAIR > (existing name) > id::text.
-- exchange: denormalized short exchange label, computed by trigger:
--   exchanges.acronym (via exchange_mic) > OPENFIGI_TICKER domain > ''.
CREATE TABLE instruments (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  asset_class  TEXT CHECK (asset_class IS NULL OR asset_class IN ('STOCK','ETF','FIXED_INCOME','MUTUAL_FUND','OPTION','FUTURE','CASH','FX','UNKNOWN')),
  exchange_mic TEXT REFERENCES exchanges(mic),
  currency     TEXT,
  name         TEXT,
  exchange     TEXT NOT NULL DEFAULT '',
  underlying_id UUID REFERENCES instruments (id),
  valid_from   DATE,
  valid_to     DATE,
  cik          TEXT,
  sic_code     TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT chk_underlying_required CHECK (
    (asset_class IN ('OPTION','FUTURE') AND underlying_id IS NOT NULL)
    OR (asset_class IS NULL OR asset_class NOT IN ('OPTION','FUTURE'))
  )
);

CREATE INDEX idx_instruments_underlying_id ON instruments (underlying_id);

-- Identifiers for an instrument. (identifier_type, domain, value) is unique globally.
-- identifier_type: proto IdentifierType name (ISIN, CUSIP, TICKER, OPENFIGI_GLOBAL, OPENFIGI_SHARE_CLASS, OPENFIGI_COMPOSITE, BROKER_DESCRIPTION, etc.).
-- domain: optional; for BROKER_DESCRIPTION = source (e.g. 'Fidelity:web:fidelity-csv'); for TICKER = exchange code when present.
-- canonical = false only for BROKER_DESCRIPTION identifiers; canonical = true for standard identifiers.
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

-- Trigger: recompute instruments.name and instruments.exchange whenever identifiers
-- or the instrument itself change. Fires AFTER so that all rows are visible.
CREATE OR REPLACE FUNCTION recompute_instrument_name() RETURNS TRIGGER AS $$
DECLARE
  instr_id UUID;
BEGIN
  IF TG_TABLE_NAME = 'instrument_identifiers' THEN
    instr_id := COALESCE(NEW.instrument_id, OLD.instrument_id);
  ELSE
    instr_id := NEW.id;
  END IF;

  UPDATE instruments SET
    name = COALESCE(
      (SELECT ii.value FROM instrument_identifiers ii
       WHERE ii.instrument_id = instr_id
         AND ii.identifier_type IN ('MIC_TICKER','OPENFIGI_TICKER','OCC','BROKER_DESCRIPTION','CURRENCY','FX_PAIR')
       ORDER BY CASE ii.identifier_type
         WHEN 'MIC_TICKER' THEN 0 WHEN 'OPENFIGI_TICKER' THEN 1
         WHEN 'OCC' THEN 2 WHEN 'BROKER_DESCRIPTION' THEN 3
         WHEN 'CURRENCY' THEN 4 WHEN 'FX_PAIR' THEN 5
       END, ii.domain, ii.value LIMIT 1),
      NULLIF(instruments.name, ''),
      instr_id::text
    ),
    exchange = COALESCE(
      (SELECT e.acronym FROM exchanges e WHERE e.mic = instruments.exchange_mic),
      (SELECT ii.domain FROM instrument_identifiers ii
       WHERE ii.instrument_id = instr_id AND ii.identifier_type = 'OPENFIGI_TICKER'
         AND ii.domain IS NOT NULL AND ii.domain <> ''
       ORDER BY ii.domain LIMIT 1),
      ''
    )
  WHERE id = instr_id;

  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- Recompute on identifier changes.
CREATE TRIGGER trg_recompute_instrument_name_on_ident
  AFTER INSERT OR UPDATE OR DELETE ON instrument_identifiers
  FOR EACH ROW EXECUTE FUNCTION recompute_instrument_name();

-- Recompute on instrument creation or exchange_mic change.
-- Column-specific UPDATE OF avoids infinite loop (trigger only writes name/exchange).
CREATE TRIGGER trg_recompute_instrument_name_on_inst
  AFTER INSERT OR UPDATE OF exchange_mic ON instruments
  FOR EACH ROW EXECUTE FUNCTION recompute_instrument_name();

-- Plugin config: which plugins are enabled, precedence (unique per category), plugin-specific config.
-- category: 'identifier', 'description', 'price'.
-- Precedence constraints are DEFERRABLE so that two plugins' precedences can be swapped
-- within a single transaction without hitting a uniqueness violation mid-swap.
-- max_history_days is only used by price plugins; NULL = unlimited lookback.
CREATE TABLE plugin_config (
  plugin_id        TEXT NOT NULL,
  category         TEXT NOT NULL CHECK (category IN ('identifier', 'description', 'price', 'inflation')),
  enabled          BOOLEAN NOT NULL DEFAULT true,
  precedence       INT NOT NULL,
  config           JSONB,
  max_history_days INT,
  PRIMARY KEY (plugin_id, category),
  UNIQUE (category, precedence) DEFERRABLE INITIALLY IMMEDIATE
);

-- Blocked (instrument, plugin) pairs that should not be retried.
CREATE TABLE price_fetch_blocks (
  instrument_id UUID NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
  plugin_id     TEXT NOT NULL,
  reason        TEXT NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (instrument_id, plugin_id)
);

-- Monthly inflation index values per currency. Index values are relative to a
-- base year where July 1st = 100. Different providers/currencies may use
-- different base years (e.g. ONS CPIH uses 2015=100).
CREATE TABLE inflation_indices (
  currency      TEXT        NOT NULL,              -- ISO 4217 (e.g. 'GBP')
  month         DATE        NOT NULL,              -- 1st of month UTC
  index_value   NUMERIC     NOT NULL,              -- relative to base_year July=100
  base_year     INT         NOT NULL,              -- year where July = 100
  data_provider TEXT        NOT NULL,              -- plugin ID
  fetched_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (currency, month)
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

CREATE UNIQUE INDEX idx_txs_initialize_unique
  ON txs (user_id, broker, account, instrument_id)
  WHERE synthetic_purpose = 'INITIALIZE';

-- Portfolio filter matching view: returns (portfolio_id, tx_id) pairs for txs
-- matching the portfolio's filters. Semantics: AND between categories (broker,
-- account, instrument), OR within each category. Categories with no filters are
-- unconstrained. Portfolios with zero filters match zero transactions.
CREATE VIEW portfolio_matched_txs AS
SELECT p.id AS portfolio_id, t.id AS tx_id
FROM portfolios p
JOIN txs t ON t.user_id = p.user_id
WHERE
  EXISTS (SELECT 1 FROM portfolio_filters WHERE portfolio_id = p.id)
  AND (NOT EXISTS (SELECT 1 FROM portfolio_filters WHERE portfolio_id = p.id AND filter_type = 'broker')
       OR t.broker IN (SELECT filter_value FROM portfolio_filters WHERE portfolio_id = p.id AND filter_type = 'broker'))
  AND (NOT EXISTS (SELECT 1 FROM portfolio_filters WHERE portfolio_id = p.id AND filter_type = 'account')
       OR t.account IN (SELECT filter_value FROM portfolio_filters WHERE portfolio_id = p.id AND filter_type = 'account'))
  AND (NOT EXISTS (SELECT 1 FROM portfolio_filters WHERE portfolio_id = p.id AND filter_type = 'instrument')
       OR (t.instrument_id IS NOT NULL
           AND t.instrument_id::text IN (SELECT filter_value FROM portfolio_filters WHERE portfolio_id = p.id AND filter_type = 'instrument')));

-- EOD price cache. Stores end-of-day OHLCV data per instrument per date.
-- Rows with synthetic = true are forward-filled (LOCF) prices for non-trading
-- days (weekends, holidays). They are generated at write time by the price
-- fetcher worker so that the valuation query can use a simple join.
CREATE TABLE eod_prices (
  instrument_id   UUID        NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  price_date      DATE        NOT NULL,
  open            NUMERIC,
  high            NUMERIC,
  low             NUMERIC,
  close           NUMERIC     NOT NULL,
  adjusted_close  NUMERIC,
  volume          BIGINT,
  data_provider   TEXT        NOT NULL,
  synthetic       BOOLEAN     NOT NULL DEFAULT false,
  fetched_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (instrument_id, price_date)
);

SELECT create_hypertable('eod_prices', 'price_date');

-- Holding declarations: user-provided statement of known holding quantity at a date.
-- Holdings are computed aggregates identified by (broker, account, instrument_id).
CREATE TABLE holding_declarations (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  broker          TEXT NOT NULL,
  account         TEXT NOT NULL,
  instrument_id   UUID NOT NULL REFERENCES instruments(id),
  declared_qty    NUMERIC NOT NULL,
  as_of_date      DATE NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, broker, account, instrument_id)
);

-- Ignored asset classes: skip tx types mapping to these asset classes during ingestion.
-- account = '' means all accounts for the broker; otherwise a specific broker+account pair.
CREATE TABLE ignored_asset_classes (
  user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  broker      TEXT NOT NULL,
  account     TEXT NOT NULL DEFAULT '',
  asset_class TEXT NOT NULL,
  PRIMARY KEY (user_id, broker, account, asset_class)
);

CREATE INDEX idx_ignored_asset_classes_user ON ignored_asset_classes (user_id);

-- qty_is_zero returns true when a double precision quantity is effectively zero,
-- absorbing floating-point residuals from SUM aggregation over buys/sells.
CREATE FUNCTION qty_is_zero(q double precision) RETURNS boolean
    LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT q IS NULL OR ABS(q) < 1e-9
$$;
