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

-- A tx_group is one economic event; the txs referencing it are its postings.
-- timestamp is the event date; the postings carry the amounts and accounts.
-- job_id is the ingestion job that created the group, and is NULL for
-- system-generated groups such as the one backing an INITIALIZE posting.
-- It is deliberately not a foreign key: a group must outlive its job, and if job
-- rows are ever pruned by age the id still distinguishes one creation from another
-- and still groups everything written by the same upload.
-- The postings of a group are required to sum to zero in every commodity they weigh
-- in, enforced by check_tx_group_balance() at COMMIT.
-- See docs/spec/postings.md.
CREATE TABLE tx_groups (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  timestamp  TIMESTAMPTZ NOT NULL,
  job_id     UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tx_groups_user_time ON tx_groups (user_id, timestamp);
CREATE INDEX idx_tx_groups_job_id ON tx_groups (job_id);

-- Transactions. Each row is a posting: a signed amount of one commodity in one account
-- at one point in time. See docs/spec/postings.md. The sections below follow the column
-- order of the table.
--
-- Ingestion and identity.
-- No natural key (broker statements often supply date only). Bulk idempotency is by
-- replace-by-period (user_id, broker, period); single-tx ingestion is append-only.
-- See docs/adr/0002-transaction-ingestion-model.md.
--
-- What moved: instrument_description, instrument_id, tx_type.
-- instrument_id is added by an ALTER further down, because instruments is created after
-- this table and the foreign key needs it to exist. The column itself is declared here,
-- where it belongs.
--
-- The amounts as the source wrote them: quantity, unit_price, trading_currency,
-- settlement_currency. Transcribed and exact; see Numeric types below.
--
-- What the source called this row: broker_ref, counterparty_account.
-- broker_ref is the source's own identifier for the row a posting was transcribed from:
-- Fidelity's Reference Number, OFX's FITID. counterparty_account is the account the
-- source named as the other side, in the same broker. Both are set only on postings
-- transcribed from a source row -- never on a converter's derived counter-leg and never
-- on a routed residual -- so a non-null value always names something the source itself
-- issued.
-- broker_ref is not a natural key and carries no uniqueness constraint: ingestion
-- idempotency is by replacement, and one source transaction can produce several
-- postings that share a reference. Transfer matching reads it as a proximity signal,
-- because a broker issues the two sides of one transfer adjacent references.
-- counterparty_account is advisory rather than authoritative. A source can reuse the
-- same field for something else -- Fidelity puts the product account a service fee was
-- charged for in it, which is attribution and not a transfer counterparty -- so it is
-- read as a pointer only for a group that produced a TRANSFER_CLEARING residual.
--
-- What kind of leg it is: account_type, synthetic_purpose.
-- account_type classifies the account this row lands in. USER is an ordinary broker
-- account posting; the others are the non-asset side of an event that is one-sided in
-- the source data and keep the broker and account of the event they belong to, so a
-- residual stays attributable to the account that produced it. Holdings and the other
-- quantity aggregations read USER only. See docs/adr/0022-typed-per-account-cash-flow-boundary.md.
--
-- The event and its balance: group_id, weight, weight_commodity.
-- group_id is the economic event this row is a posting of. Every posting belongs to
-- exactly one group -- a lone posting is a group of one -- so that the balance
-- invariant has no rows it cannot reach. Deleting a group deletes its postings, which
-- makes the group the unit of deletion for replace-by-period.
-- weight is what this posting contributes to its group's balance and weight_commodity
-- names what that contribution is denominated in: 'cur:<code>' for money,
-- 'inst:<uuid>' for a security, 'desc:<text>' when the instrument never resolved. A
-- group balances when SUM(weight) is zero for each weight_commodity. The weight rules
-- live in server/service/ingestion/balance.go and are evaluated once at ingest; the
-- result is stored rather than re-derived here, because instrument state moves under a
-- posting afterwards -- a merge rewrites instrument_id and contract_multiplier records
-- what a corporate action left behind -- so a re-derived weight could disagree with the
-- one the group was balanced on. Computed from the raw quantity and unit_price, never
-- from the split-adjusted pair, which carries a rounding an exact check would reject.
-- The merge in server/db/postgres/instruments.go is the only thing that maintains
-- these after ingest, and it rewrites weight_commodity alongside instrument_id.
-- See docs/adr/0029-posting-weight-is-stored.md and
-- docs/adr/0024-group-balance-is-checked-on-weight.md.
--
-- Share counts: share_count_basis, split_adjusted_quantity, split_adjusted_unit_price.
-- share_count_basis is the date at which the share count the raw quantity and
-- unit_price are denominated in was current. It defaults to timestamp::date --
-- the as-traded assumption, that a broker log line accounts only for events
-- prior to the trade. A source that restates historical rows (a broker's live
-- web UI showing post-split quantities) declares its own on the upload.
-- See docs/spec/bitemporality.md.
-- split_adjusted_quantity / split_adjusted_unit_price hold the values that result
-- from applying every stock split with ex_date > share_count_basis for the tx's
-- instrument. They equal the raw quantity/unit_price when no later split exists.
-- They are recomputed idempotently from the raw columns whenever splits change
-- (see RecomputeTxSplitAdjustments).
--
-- Numeric types.
-- quantity and unit_price are transcribed decimals and are exact, so they are
-- bare NUMERIC. The split-adjusted pair is not: the cumulative split factor is a
-- rational and a reverse /3 has no finite decimal form, so the pair declares a
-- rounding scale of 12 -- more places than any broker quotes fractional shares or
-- prices to. The rounding is confined to this derived cache; the raw columns it
-- is computed from stay exact and it is recomputable from them at any time. An
-- exact check (a group balance, a checked declaration) reads the raw columns.
-- See docs/adr/0026-exact-decimals-bounded-by-closure.md and
-- docs/adr/0028-cumulative-split-factor-is-an-exact-rational.md.
CREATE TABLE txs (
  id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id                   UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,

  broker                    TEXT NOT NULL,
  account                   TEXT NOT NULL,
  timestamp                 TIMESTAMPTZ NOT NULL,

  instrument_description    TEXT NOT NULL,
  instrument_id             UUID,
  tx_type                   TEXT NOT NULL,

  quantity                  NUMERIC NOT NULL,
  unit_price                NUMERIC,
  trading_currency          TEXT,
  settlement_currency       TEXT,

  broker_ref                TEXT,
  counterparty_account      TEXT,

  account_type              TEXT NOT NULL DEFAULT 'USER'
                              CHECK (account_type IN ('USER', 'EQUITY', 'INCOME', 'EXPENSE',
                                                      'IMBALANCE', 'TRANSFER_CLEARING',
                                                      'SOURCE_ROUNDING')),
  synthetic_purpose         TEXT CHECK (synthetic_purpose IS NULL OR synthetic_purpose = 'INITIALIZE'),

  group_id                  UUID NOT NULL REFERENCES tx_groups (id) ON DELETE CASCADE,
  weight                    NUMERIC NOT NULL,
  weight_commodity          TEXT NOT NULL,

  share_count_basis         DATE NOT NULL,
  split_adjusted_quantity   NUMERIC(38, 12) NOT NULL,
  split_adjusted_unit_price NUMERIC(38, 12),

  created_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_txs_user_broker_time ON txs (user_id, broker, timestamp);
CREATE INDEX idx_txs_group_id ON txs (group_id);

-- Async ingestion jobs. status and validation_errors surfaced via front-end API.
-- job_type distinguishes tx uploads from archive imports; broker/source are
-- tx-specific.
-- payload stores the serialized protobuf request and is cleared after processing.
CREATE TABLE ingestion_jobs (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  job_type     TEXT NOT NULL DEFAULT 'tx' CHECK (job_type IN ('tx', 'system_archive', 'user_archive')),
  broker       TEXT,
  source       TEXT,
  filename     TEXT,
  period_from   TIMESTAMPTZ,
  period_before TIMESTAMPTZ,
  status       TEXT NOT NULL CHECK (status IN ('PENDING', 'RUNNING', 'SUCCESS', 'FAILED')),
  total_count      INT NOT NULL DEFAULT 0,
  processed_count  INT NOT NULL DEFAULT 0,
  payload      BYTEA,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_ingestion_jobs_user ON ingestion_jobs (user_id);
CREATE INDEX idx_ingestion_jobs_status ON ingestion_jobs (status);

-- Per-part results for an archive import. One row per part the document
-- carried, created with the job so a page can render a row per part before the
-- worker has started and after a reload. part spells the ArchivePart enum name,
-- so the proto value, the column and the archive section are one string.
--
-- Restore order is a property of the archive format rather than of a row, so
-- there is no ordinal column: readers order by the enum.
CREATE TABLE ingestion_job_parts (
  job_id          UUID NOT NULL REFERENCES ingestion_jobs (id) ON DELETE CASCADE,
  part            TEXT NOT NULL CHECK (part IN ('INSTRUMENTS', 'PRICES', 'CORPORATE_EVENTS',
                                                'INFLATION_INDICES', 'FETCH_BLOCKS',
                                                'UNHANDLED_EVENTS', 'PLUGIN_CONFIG',
                                                'PREFERENCES', 'TXS')),
  status          TEXT NOT NULL CHECK (status IN ('PENDING', 'RUNNING', 'SUCCESS', 'FAILED')),
  total_count     INT NOT NULL DEFAULT 0,
  processed_count INT NOT NULL DEFAULT 0,
  message         TEXT,
  PRIMARY KEY (job_id, part)
);

-- Validation errors for async ingestion. row_index, field, message per API.
-- part attributes an error to one part of an archive; NULL for a tx job
-- and for a failure that happened before any part was reached.
CREATE TABLE validation_errors (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  job_id     UUID NOT NULL REFERENCES ingestion_jobs (id) ON DELETE CASCADE,
  part       TEXT,
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
  -- The half-open [valid_from, valid_before) interval the instrument was
  -- tradeable in. Descriptive only: no query filters on these, and no
  -- identifier plugin supplies them yet. Identity is resolved as current
  -- state. See docs/spec/bitemporality.md.
  valid_from   DATE,
  valid_before DATE,
  cik          TEXT,
  sic_code     TEXT,
  -- Denormalized from the OCC identifier for options. NULL for non-options.
  strike       NUMERIC,
  expiry       DATE,
  put_call     TEXT CHECK (put_call IS NULL OR put_call IN ('C', 'P')),
  -- Deliverable multiplier: 1 = standard (100 shares/contract). Non-standard
  -- splits (e.g. 3:2) may set this to 1.5 meaning 150 shares/contract.
  contract_multiplier NUMERIC NOT NULL DEFAULT 1,
  -- The point in market time the stored identity reflects: the state the OCC
  -- symbol, strike and contract terms were derived from. Moves only on genuine
  -- re-derivation -- plugin identification, or a retroactive option split
  -- adjustment -- never on an incidental EnsureInstrument touch. Compared
  -- against stock_splits.ex_date to decide whether an option still needs
  -- retroactive adjustment: providers list the pre-split OCC symbol until the
  -- ex_date, so an identity derived before then does not reflect the split.
  -- NULL means the identity predates every split. Bounded by expiry: a split
  -- restates only the contracts listed on its effective date, so a split with
  -- ex_date after expiry never reached this contract. See
  -- docs/spec/bitemporality.md,
  -- docs/adr/0017-option-identity-reflects-ex-date.md and
  -- docs/adr/0036-expired-options-are-not-restated.md.
  identity_as_of TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT chk_underlying_required CHECK (
    (asset_class IN ('OPTION','FUTURE') AND underlying_id IS NOT NULL)
    OR (asset_class IS NULL OR asset_class NOT IN ('OPTION','FUTURE'))
  ),
  CONSTRAINT chk_option_fields CHECK (
    (asset_class = 'OPTION' AND strike IS NOT NULL AND expiry IS NOT NULL AND put_call IS NOT NULL)
    OR asset_class IS DISTINCT FROM 'OPTION'
  )
);

CREATE INDEX idx_instruments_underlying_id ON instruments (underlying_id);

-- Identifiers for an instrument. (identifier_type, domain, value) is unique globally.
-- identifier_type: proto IdentifierType name (ISIN, CUSIP, TICKER, OPENFIGI_GLOBAL, OPENFIGI_SHARE_CLASS, OPENFIGI_COMPOSITE, BROKER_DESCRIPTION, etc.).
-- domain: optional; for BROKER_DESCRIPTION = source (e.g. 'Fidelity:web:fidelity-csv'); for TICKER = exchange code when present.
-- canonical = false only for BROKER_DESCRIPTION identifiers; canonical = true for standard identifiers.
-- Surrogate PK so domain can be NULL (PostgreSQL PK columns are NOT NULL).
-- Identifiers carry no validity interval by design: the triple is unique for all
-- time, so ticker reuse is not representable and a merge deletes the loser
-- rather than closing its interval. See docs/adr/0004-instrument-resolution-and-merge.md.
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

-- Provider-specific instrument identifiers. Mirrors instrument_identifiers but
-- scoped to a data provider. Identifier types are free-form strings specific to
-- the provider (e.g. "SEGMENT_MIC_TICKER", "EODHD_EXCH_CODE", "FIGI").
CREATE TABLE provider_instrument_identifiers (
  id              UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
  instrument_id   UUID NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  provider        TEXT NOT NULL,
  identifier_type TEXT NOT NULL,
  domain          TEXT,
  value           TEXT NOT NULL
);

-- Per-instrument per-provider uniqueness.
CREATE UNIQUE INDEX idx_prov_instr_ident_unique_null_domain ON provider_instrument_identifiers (instrument_id, provider, identifier_type, value) WHERE domain IS NULL;
CREATE UNIQUE INDEX idx_prov_instr_ident_unique_non_null_domain ON provider_instrument_identifiers (instrument_id, provider, identifier_type, domain, value) WHERE domain IS NOT NULL;
-- Reverse lookup by provider + identifier.
CREATE INDEX idx_prov_instr_ident_lookup ON provider_instrument_identifiers (provider, identifier_type, value);

-- Plugin config: which plugins are enabled, precedence (unique per category), plugin-specific config.
-- category: 'identifier', 'description', 'price'.
-- Precedence constraints are DEFERRABLE so that two plugins' precedences can be swapped
-- within a single transaction without hitting a uniqueness violation mid-swap.
-- max_history_days is only used by price plugins; NULL = unlimited lookback.
CREATE TABLE plugin_config (
  plugin_id        TEXT NOT NULL,
  category         TEXT NOT NULL CHECK (category IN ('identifier', 'description', 'price', 'inflation', 'corporate_event')),
  enabled          BOOLEAN NOT NULL DEFAULT true,
  precedence       INT NOT NULL,
  config           JSONB,
  max_history_days INT,
  PRIMARY KEY (plugin_id, category),
  UNIQUE (category, precedence) DEFERRABLE INITIALLY IMMEDIATE
);

-- Blocked (instrument, plugin) pairs that should not be retried.
-- first_blocked_at is when the pair was first blocked and is never overwritten;
-- re-blocking updates only the reason. See docs/spec/bitemporality.md.
CREATE TABLE price_fetch_blocks (
  instrument_id   UUID NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
  plugin_id       TEXT NOT NULL,
  reason          TEXT NOT NULL,
  first_blocked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
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
  -- Staleness only: overwritten on every refresh. Index values are not
  -- versioned by design: a revision replaces its predecessor in place and
  -- leaves no record. See docs/spec/bitemporality.md.
  last_fetched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
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
-- The column is declared with the rest of txs; only the foreign key waits until here,
-- because instruments is created after txs.
ALTER TABLE txs ADD CONSTRAINT txs_instrument_id_fkey
  FOREIGN KEY (instrument_id) REFERENCES instruments (id);

CREATE INDEX idx_txs_instrument_id ON txs (instrument_id);

-- Residual postings -- the IMBALANCE, TRANSFER_CLEARING and SOURCE_ROUNDING legs
-- routed to balance a group its source data left one-sided -- are a small minority of
-- txs, and the report that aggregates them reads every one across all users. A partial
-- index over just those rows answers it without carrying the USER postings that
-- dominate the table. Column order follows the report's GROUP BY.
CREATE INDEX idx_txs_residual_postings
  ON txs (account_type, user_id, broker, account, instrument_id, tx_type)
  WHERE account_type IN ('IMBALANCE', 'TRANSFER_CLEARING', 'SOURCE_ROUNDING');

-- At most one INITIALIZE posting per holding per account type. account_type is part of
-- the key because the pad's counterparty is an equal-and-opposite posting of the same
-- instrument in the same broker account, and is synthetic for the same reason the pad
-- is; without it the two collide.
CREATE UNIQUE INDEX idx_txs_initialize_unique
  ON txs (user_id, broker, account, instrument_id, account_type)
  WHERE synthetic_purpose = 'INITIALIZE';

-- The two sides of a transfer, paired after the fact.
--
-- Each side of a journal is balanced by a TRANSFER_CLEARING counterparty and the two
-- are deliberately not paired at ingest, because brokers report them in separate
-- statements and sometimes in separate imports. This is the pairing. It records which
-- group -- and so which account -- holds the other side, because that identity is what
-- the portfolio membership test in docs/adr/0022-typed-per-account-cash-flow-boundary.md
-- consumes; that a match merely exists would not answer it.
--
-- A link rather than a status column on the posting, which could say that a side was
-- matched but not what it matched with. And not a synthetic third group closing both
-- sides out, which would fabricate an economic event that never happened and have to be
-- unpicked on rematch. Both alternatives are also unreachable: check_tx_group_balance()
-- rejects mutating or deleting a single leg of a group.
--
-- Derived and disposable. A re-upload replaces one side's groups
-- (docs/adr/0002-transaction-ingestion-model.md), the cascade takes the link with them,
-- the surviving side reappears as unmatched and the matcher runs again. It is never
-- authoritative and is always cheap to rebuild.
--
-- Directed. from_group_id is the side the value left: its TRANSFER_CLEARING residual is
-- positive, because the group's own leg is negative and the clearing leg holds what is
-- owed out. to_group_id is where it arrived. The direction costs nothing to record --
-- it is the sign of the residual -- and a link that did not carry it would read the
-- same for a transfer in either direction.
--
-- Keyed per (group, commodity) rather than per group, which is what the two unique
-- indexes say. Balancing emits one residual per commodity, so an unpaired JRNLSEC group
-- can have a security side and a cash side in flight independently and each pairs with
-- a different group. instrument_id moves with an instrument merge, alongside
-- txs.instrument_id.
--
-- Neither the quantity nor the ingestion job is stored. The two sides are equal and
-- opposite by construction, so a stored amount could only disagree with them, and a
-- match is not made by an ingestion job.
--
-- user_id must be the owner of both groups, and both groups must have the same owner.
-- Neither is checked here: a CHECK cannot reach another table and a trigger to enforce
-- it would run on every insert to catch a case no caller can produce. The matcher
-- partitions its candidates by user before proposing anything, so a cross-user pair
-- cannot be built. The column is a denormalisation for query scoping -- deleting a
-- user already cascades through tx_groups -- and the invariant is the caller's.
CREATE TABLE transfer_matches (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  from_group_id UUID NOT NULL REFERENCES tx_groups (id) ON DELETE CASCADE,
  to_group_id   UUID NOT NULL REFERENCES tx_groups (id) ON DELETE CASCADE,
  instrument_id UUID NOT NULL REFERENCES instruments (id),
  -- How the pair was found. POINTER is the source naming the other account outright;
  -- REFERENCE is the proximity of the two sides' broker references; MANUAL is a person
  -- saying so. There is no amount-and-window-alone value: a pair with no evidence
  -- beyond an equal and opposite amount is left unmatched rather than guessed at.
  method        TEXT NOT NULL CHECK (method IN ('POINTER', 'REFERENCE', 'MANUAL')),
  matched_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (from_group_id <> to_group_id)
);

-- One match per side per commodity, in both directions. These are the uniqueness
-- constraint and the lookup index at once: what holds the other side of a group is
-- (from_group_id = g OR to_group_id = g), and neither side can be matched twice.
CREATE UNIQUE INDEX idx_transfer_matches_from ON transfer_matches (from_group_id, instrument_id);
CREATE UNIQUE INDEX idx_transfer_matches_to   ON transfer_matches (to_group_id, instrument_id);

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
-- Every row is a bar a provider actually reported: non-trading days (weekends,
-- holidays) simply have no row. Valuation carries the last close forward over
-- them at read time, bounded by price_coverage, so the filled series is derived
-- from (bars, coverage) rather than stored alongside them.
-- share_count_basis is the date at which the share count the raw OHLCV is
-- denominated in was current. It is declared by whoever supplied the row -- as
-- price_date for an as-traded series, or the fetch date for a provider that
-- back-adjusts -- and is never inferred from last_fetched_at. It defaults to
-- price_date, the as-traded assumption. See docs/spec/bitemporality.md.
-- The split_adjusted_* columns hold OHLCV after applying every stock split with
-- ex_date > share_count_basis for this instrument. They equal the raw values when
-- no later split exists. close (NOT NULL) implies split_adjusted_close (NOT NULL);
-- the others are NULL iff their raw counterpart is NULL. Volume is adjusted in
-- the opposite direction (more shares trade in adjusted-share terms).
-- The raw OHLC are transcribed decimals and are exact, so they are bare NUMERIC.
-- The split_adjusted_* pair is not: the cumulative split factor is a rational and
-- a reverse /3 has no finite decimal form, so they declare a rounding scale of 12,
-- matching txs.split_adjusted_*. The rounding is confined to this derived cache.
-- See docs/adr/0028-cumulative-split-factor-is-an-exact-rational.md.
-- adjusted_close is preserved as-supplied by the data provider on the provider's
-- own basis (typically including dividend adjustment as well as splits). It is
-- never an input to valuation and exists to cross-check split_adjusted_close,
-- which is the value PortfolioDB derives itself.
CREATE TABLE eod_prices (
  instrument_id          UUID        NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  price_date             DATE        NOT NULL,
  open                   NUMERIC,
  split_adjusted_open    NUMERIC(38, 12),
  high                   NUMERIC,
  split_adjusted_high    NUMERIC(38, 12),
  low                    NUMERIC,
  split_adjusted_low     NUMERIC(38, 12),
  close                  NUMERIC     NOT NULL,
  split_adjusted_close   NUMERIC(38, 12) NOT NULL,
  adjusted_close         NUMERIC,
  volume                 BIGINT,
  split_adjusted_volume  BIGINT,
  data_provider          TEXT        NOT NULL,
  share_count_basis      DATE        NOT NULL,
  -- Staleness only: when this row was last fetched. It carries no meaning about
  -- the prices themselves. See docs/spec/bitemporality.md.
  last_fetched_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (instrument_id, price_date)
);

SELECT create_hypertable('eod_prices', 'price_date');

-- Coverage tracking for prices. A missing eod_prices row does not say whether
-- the date was never fetched or was fetched and had no price (pre-IPO,
-- delisted, suspended, or beyond a plugin's history limit). This table records,
-- per (instrument, plugin), the date intervals the plugin has answered
-- authoritatively -- including answers that returned no bars at all, which are
-- coverage just as much as a full series is.
-- The interval is half-open [covered_from, covered_before). Adjacent or
-- overlapping intervals for the same (instrument, plugin) are merged on insert.
-- Every eod_prices row lies within some span here for its instrument; the
-- converse does not hold, and a span with no rows in it is exactly the "asked,
-- nothing there" answer that row presence alone cannot express.
CREATE TABLE price_coverage (
  instrument_id  UUID        NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  plugin_id      TEXT        NOT NULL,
  covered_from   DATE        NOT NULL,
  covered_before DATE        NOT NULL CHECK (covered_before > covered_from),
  -- Staleness only: when this span was last confirmed. A merged span keeps the
  -- oldest constituent value, since a union is only as freshly confirmed as its
  -- stalest part.
  last_fetched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (instrument_id, plugin_id, covered_from)
);

CREATE INDEX idx_price_coverage_instrument ON price_coverage (instrument_id);

-- Coverage merged across plugins. Every caller asks the same question of this
-- table -- which date spans has anyone answered for -- and none of them cares
-- who answered: the valuation carry-forward needs a bound, the gap scan needs
-- what is already answered for, and the export needs what to write down. So the
-- per-plugin rows are unioned into the fewest non-overlapping spans covering
-- the same dates, once, here.
--
-- range_agg over a half-open daterange merges adjacent spans as well as
-- overlapping ones, which matters to a carry-forward partitioned by span: two
-- plugins covering [Jan, Feb) and [Feb, Mar) must come out as one span, or the
-- spurious boundary restarts the fill and the first day of February reads as
-- unpriced.
--
-- last_fetched_at is deliberately not carried. A merged span has no single
-- constituent to take it from, and no caller needs it yet. When one does, the
-- staleness rule stated on the table above -- a union is only as freshly
-- confirmed as its stalest part -- belongs here rather than at the call site.
--
-- A view, not a repeated subquery, so the merge has one definition. Postgres
-- inlines it, so a caller's instrument_id predicate still reaches the aggregate:
-- instrument_id is the grouping key.
CREATE VIEW merged_price_coverage AS
SELECT instrument_id,
       lower(span) AS covered_from,
       upper(span) AS covered_before
FROM (
  SELECT instrument_id,
         unnest(range_agg(daterange(covered_from, covered_before))) AS span
  FROM price_coverage
  GROUP BY instrument_id
) s;

-- Stock splits per instrument. ex_date is the effective/execution date. The
-- split factor is split_to / split_from (e.g. 2:1 split = split_from=1,
-- split_to=2, factor=2; 1:2 reverse split = split_from=2, split_to=1, factor=0.5).
-- data_provider is the plugin id ("massive", "eodhd", ...), "import" for CSV/JSON
-- imports, or "broker:<broker>" for events sourced from a broker SPLIT tx.
CREATE TABLE stock_splits (
  instrument_id  UUID        NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  ex_date        DATE        NOT NULL,
  split_from     NUMERIC     NOT NULL CHECK (split_from > 0),
  split_to       NUMERIC     NOT NULL CHECK (split_to   > 0),
  data_provider  TEXT        NOT NULL,
  -- When we first learned of this split. It only ever moves backwards: revising
  -- the ratio leaves it alone, and an import supplying an earlier stamp restores
  -- that one. It is preserved across corporate-event export and import; option
  -- adjustment keys off ex_date rather than this column, because what matters
  -- there is when the split took effect, not when we heard about it.
  first_known_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (instrument_id, ex_date)
);

CREATE INDEX idx_stock_splits_instrument ON stock_splits (instrument_id);

-- Cash dividends per instrument. ex_date is the ex-dividend date. amount is per
-- share, denominated in currency (which may differ from the instrument currency
-- for cross-listed securities). frequency is provider-supplied and may be NULL.
CREATE TABLE cash_dividends (
  instrument_id    UUID        NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  ex_date          DATE        NOT NULL,
  pay_date         DATE,
  record_date      DATE,
  declaration_date DATE,
  amount           NUMERIC     NOT NULL CHECK (amount >= 0),
  currency         TEXT        NOT NULL,
  frequency        TEXT,
  type             TEXT        NOT NULL DEFAULT 'CD',
  data_provider    TEXT        NOT NULL,
  -- When we first learned of this dividend. Only ever moves backwards, as for
  -- stock_splits.first_known_at.
  first_known_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (instrument_id, ex_date)
);

CREATE INDEX idx_cash_dividends_instrument ON cash_dividends (instrument_id);

-- Coverage tracking for corporate events. Events are sparse, so absence of an
-- event for a (instrument, date) does not tell us whether the data was fetched.
-- This table records, per (instrument, plugin), the date intervals for which
-- the plugin has been queried successfully (including queries that returned
-- empty results -- those are still authoritative coverage).
-- The interval is half-open [covered_from, covered_before). Adjacent or
-- overlapping intervals for the same (instrument, plugin) are merged on insert.
CREATE TABLE corporate_event_coverage (
  instrument_id  UUID        NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  plugin_id      TEXT        NOT NULL,
  covered_from   DATE        NOT NULL,
  covered_before DATE        NOT NULL CHECK (covered_before > covered_from),
  -- Staleness only: when this span was last confirmed. A merged span keeps the
  -- oldest constituent value, since a union is only as freshly confirmed as its
  -- stalest part.
  last_fetched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (instrument_id, plugin_id, covered_from)
);

CREATE INDEX idx_corporate_event_coverage_instrument ON corporate_event_coverage (instrument_id);

-- Coverage merged across plugins, as merged_price_coverage is for prices. See
-- the comment there for why the merge lives in a view.
CREATE VIEW merged_corporate_event_coverage AS
SELECT instrument_id,
       lower(span) AS covered_from,
       upper(span) AS covered_before
FROM (
  SELECT instrument_id,
         unnest(range_agg(daterange(covered_from, covered_before))) AS span
  FROM corporate_event_coverage
  GROUP BY instrument_id
) s;

-- Blocked (instrument, plugin) pairs for corporate-event fetches. Mirrors
-- price_fetch_blocks: an entry here means the plugin returned a permanent
-- error (404, 403, ...) for this instrument and should not be retried.
-- first_blocked_at is never overwritten; re-blocking updates only the reason.
CREATE TABLE corporate_event_fetch_blocks (
  instrument_id    UUID        NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  plugin_id        TEXT        NOT NULL,
  reason           TEXT        NOT NULL,
  first_blocked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (instrument_id, plugin_id)
);

-- Corporate events that cannot be automatically processed (reverse splits,
-- non-whole splits, mergers, extraordinary dividends on options, futures
-- adjustments). Surfaced to admin users for manual review.
CREATE TABLE unhandled_corporate_events (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  instrument_id UUID        NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  event_type    TEXT        NOT NULL,
  ex_date       DATE,
  detail        TEXT        NOT NULL,
  data          JSONB,
  resolved      BOOLEAN     NOT NULL DEFAULT false,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_unhandled_ce_unresolved
  ON unhandled_corporate_events (resolved) WHERE NOT resolved;

-- Prevent duplicate unresolved events for the same (instrument, type, date).
-- NULL ex_dates are treated as distinct by PostgreSQL unique indexes, which is
-- acceptable since events without an ex_date are rare edge cases.
CREATE UNIQUE INDEX idx_unhandled_ce_dedup
  ON unhandled_corporate_events (instrument_id, event_type, ex_date)
  WHERE NOT resolved;

-- Default the split_adjusted_* columns on txs to the raw counterparts whenever
-- they are not explicitly set. This keeps every existing INSERT/UPSERT path
-- working without modification: callers continue to set quantity / unit_price
-- and the trigger seeds split_adjusted_quantity / split_adjusted_unit_price to
-- the same value. The recompute pass (RecomputeTxSplitAdjustments) later
-- overwrites the adjusted columns when stock_splits exist for the instrument.
-- On UPDATE, the trigger only resets adjusted columns when the raw column
-- actually changes AND the adjusted column was not part of the same UPDATE,
-- so explicit recompute updates are preserved.
CREATE OR REPLACE FUNCTION default_split_adjusted_tx() RETURNS TRIGGER AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    -- As-traded is the default denomination: a row with no declared basis is
    -- assumed to be expressed in the share count current on its own date.
    IF NEW.share_count_basis IS NULL THEN
      NEW.share_count_basis := NEW.timestamp::date;
    END IF;
    IF NEW.split_adjusted_quantity IS NULL THEN
      NEW.split_adjusted_quantity := NEW.quantity;
    END IF;
    IF NEW.split_adjusted_unit_price IS NULL THEN
      NEW.split_adjusted_unit_price := NEW.unit_price;
    END IF;
  ELSIF TG_OP = 'UPDATE' THEN
    IF NEW.quantity IS DISTINCT FROM OLD.quantity
       AND NEW.split_adjusted_quantity IS NOT DISTINCT FROM OLD.split_adjusted_quantity THEN
      NEW.split_adjusted_quantity := NEW.quantity;
    END IF;
    IF NEW.unit_price IS DISTINCT FROM OLD.unit_price
       AND NEW.split_adjusted_unit_price IS NOT DISTINCT FROM OLD.split_adjusted_unit_price THEN
      NEW.split_adjusted_unit_price := NEW.unit_price;
    END IF;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_default_split_adjusted_tx
  BEFORE INSERT OR UPDATE ON txs
  FOR EACH ROW EXECUTE FUNCTION default_split_adjusted_tx();

-- The postings of a tx group must sum to zero in every commodity they weigh in.
--
-- Enforcing this here rather than in the application makes it unbypassable: no code
-- path, no bad import and no manual psql session can leave an unbalanced group
-- behind. It is affordable because ingestion routes every non-zero residual to an
-- explicit counterparty, so balance is satisfiable by construction and turning the
-- check on cannot reject otherwise-valid data.
--
-- It reads the stored weight rather than re-deriving one. The weight rules live in
-- server/service/ingestion/balance.go, and a second copy in PL/pgSQL would both drift
-- from the Go one and re-derive at COMMIT from instrument state that has moved under
-- the posting since it was written -- which could reject a group that was balanced
-- when it was written, on an update that has nothing to do with it. See
-- docs/adr/0029-posting-weight-is-stored.md.
--
-- TG_OP branching rather than a CASE over NEW/OLD, because referencing NEW in a
-- DELETE raises whether or not the branch is taken. A wholly deleted group matches no
-- rows and passes vacuously, which is what lets replace-by-period delete groups and
-- let the cascade take their postings.
CREATE OR REPLACE FUNCTION check_tx_group_balance() RETURNS TRIGGER AS $$
DECLARE
  gids UUID[] := ARRAY[]::UUID[];
  gid  UUID;
  bad  RECORD;
BEGIN
  IF TG_OP <> 'INSERT' THEN
    gids := gids || OLD.group_id;
  END IF;
  IF TG_OP <> 'DELETE' THEN
    gids := gids || NEW.group_id;
  END IF;
  FOREACH gid IN ARRAY gids LOOP
    SELECT weight_commodity, SUM(weight) AS residual INTO bad
    FROM txs
    WHERE group_id = gid
    GROUP BY weight_commodity
    HAVING SUM(weight) <> 0
    LIMIT 1;
    IF FOUND THEN
      RAISE EXCEPTION 'tx group % does not balance: % left over in %',
        gid, bad.residual, bad.weight_commodity
        USING ERRCODE = 'check_violation';
    END IF;
  END LOOP;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- Deferred to COMMIT so the legs of a group can be inserted in any order within one
-- transaction: a group is only ever balanced once all of it is written. There is
-- precedent for DEFERRABLE in this schema -- exchanges.operating_mic and the
-- plugin_config precedence uniqueness both use it.
CREATE CONSTRAINT TRIGGER trg_tx_group_balance
  AFTER INSERT OR DELETE ON txs
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION check_tx_group_balance();

-- Split into a second trigger because WHEN cannot reference OLD on an INSERT. The
-- guard is what keeps the split-adjustment recompute cheap: with no instrument filter
-- it rewrites split_adjusted_* for every posting in the table and leaves weight alone,
-- so without this every such row would queue a deferred check.
CREATE CONSTRAINT TRIGGER trg_tx_group_balance_update
  AFTER UPDATE ON txs
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW
  WHEN (OLD.weight IS DISTINCT FROM NEW.weight
     OR OLD.weight_commodity IS DISTINCT FROM NEW.weight_commodity
     OR OLD.group_id IS DISTINCT FROM NEW.group_id)
  EXECUTE FUNCTION check_tx_group_balance();

-- Same defaulting trigger for eod_prices. close (NOT NULL) implies
-- split_adjusted_close (NOT NULL); the OHLV columns are nullable on both
-- sides so the adjusted column is left NULL when the raw side is NULL.
CREATE OR REPLACE FUNCTION default_split_adjusted_eod_price() RETURNS TRIGGER AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    -- As-traded is the default denomination: a bar with no declared basis is
    -- assumed to be expressed in the share count current on its own date.
    IF NEW.share_count_basis IS NULL THEN
      NEW.share_count_basis := NEW.price_date;
    END IF;
    IF NEW.split_adjusted_open IS NULL THEN
      NEW.split_adjusted_open := NEW.open;
    END IF;
    IF NEW.split_adjusted_high IS NULL THEN
      NEW.split_adjusted_high := NEW.high;
    END IF;
    IF NEW.split_adjusted_low IS NULL THEN
      NEW.split_adjusted_low := NEW.low;
    END IF;
    IF NEW.split_adjusted_close IS NULL THEN
      NEW.split_adjusted_close := NEW.close;
    END IF;
    IF NEW.split_adjusted_volume IS NULL THEN
      NEW.split_adjusted_volume := NEW.volume;
    END IF;
  ELSIF TG_OP = 'UPDATE' THEN
    IF NEW.open IS DISTINCT FROM OLD.open
       AND NEW.split_adjusted_open IS NOT DISTINCT FROM OLD.split_adjusted_open THEN
      NEW.split_adjusted_open := NEW.open;
    END IF;
    IF NEW.high IS DISTINCT FROM OLD.high
       AND NEW.split_adjusted_high IS NOT DISTINCT FROM OLD.split_adjusted_high THEN
      NEW.split_adjusted_high := NEW.high;
    END IF;
    IF NEW.low IS DISTINCT FROM OLD.low
       AND NEW.split_adjusted_low IS NOT DISTINCT FROM OLD.split_adjusted_low THEN
      NEW.split_adjusted_low := NEW.low;
    END IF;
    IF NEW.close IS DISTINCT FROM OLD.close
       AND NEW.split_adjusted_close IS NOT DISTINCT FROM OLD.split_adjusted_close THEN
      NEW.split_adjusted_close := NEW.close;
    END IF;
    IF NEW.volume IS DISTINCT FROM OLD.volume
       AND NEW.split_adjusted_volume IS NOT DISTINCT FROM OLD.split_adjusted_volume THEN
      NEW.split_adjusted_volume := NEW.volume;
    END IF;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_default_split_adjusted_eod_price
  BEFORE INSERT OR UPDATE ON eod_prices
  FOR EACH ROW EXECUTE FUNCTION default_split_adjusted_eod_price();

-- mul is the product aggregate Postgres does not ship. Multiplication over
-- numeric is exact, so a cumulative split factor built from it is exact.
-- numeric_mul is strict and the initcond is non-null, so a NULL input is skipped
-- rather than poisoning the product, and zero input rows return 1.
CREATE AGGREGATE mul(numeric) (SFUNC = numeric_mul, STYPE = numeric, INITCOND = '1');

-- split_factor_at returns the cumulative split adjustment factor for the
-- given instrument as of the given reference date, as an exact rational: the
-- numerator is the product of split_to and the denominator the product of
-- split_from, over every stock split with ex_date strictly greater than
-- reference_date AND less than or equal to the current date.
--
-- The quotient is returned unevaluated so that callers multiply first and divide
-- once -- quantity * num / den rather than quantity * (num / den) -- which is the
-- minimal-error ordering and keeps the exact part exact for as long as possible.
-- See docs/adr/0028-cumulative-split-factor-is-an-exact-rational.md.
--
-- For derivative instruments (options, futures) that have an underlying_id,
-- the function also includes splits on the underlying instrument. This
-- avoids the need to duplicate split rows onto each derivative.
--
-- The CURRENT_DATE clause matters: corporate event plugins return splits
-- the moment they are announced, often weeks before the ex_date arrives,
-- and the corporate event fetcher's lookahead window pulls them into
-- stock_splits early. Without this clause, an announced-but-not-yet-effective
-- split would immediately scale every prior price/tx for the instrument as
-- if the split had already happened, even though the user still owns
-- pre-split shares trading at pre-split prices. With this clause, future-
-- dated splits sit inert in stock_splits until their ex_date passes, at
-- which point the next recompute pass picks them up. The corporate event
-- daily scheduler (see docs/spec/corporate-events.md) is responsible for
-- triggering that recompute when ex_dates cross.
--
-- Returns 1/1 when no effective future splits are known, so a row with no
-- later splits is unchanged by adjustment.
CREATE OR REPLACE FUNCTION split_factor_at(
  p_instrument_id UUID,
  p_reference DATE,
  OUT num NUMERIC,
  OUT den NUMERIC
) LANGUAGE sql STABLE AS $$
  SELECT mul(s.split_to), mul(s.split_from)
  FROM stock_splits s
  WHERE s.instrument_id IN (
      p_instrument_id,
      (SELECT underlying_id FROM instruments WHERE id = p_instrument_id AND underlying_id IS NOT NULL)
    )
    AND s.ex_date > p_reference
    AND s.ex_date <= CURRENT_DATE;
$$;

-- holding_qty_in_basis returns a holding's quantity over a half-open [from, before)
-- window, converted into a single share count basis, together with the two numbers
-- a caller needs to bound the rounding in that conversion.
--
-- Postings are grouped by their own share_count_basis and summed before conversion,
-- so the division happens once per distinct basis rather than once per posting. That
-- is what makes the bound stateable: each group converts by an exact rational
-- (num_b * den_d) / (den_b * num_d) -- the row's own factor to today divided by the
-- target's -- so the whole result is exact unless a group's factor is not 1/1, and
-- inexact_bases counts the groups that can carry a rounding. A caller comparing
-- against a declared quantity allows inexact_bases units in the last place of the
-- split-adjusted rounding scale, and nothing when the count is zero.
--
-- Only USER postings are read. The EQUITY counterparty of a pad shares its broker
-- account and instrument, so including it would net a declared opening balance back
-- to zero. p_include_synthetic is false when computing a pad, which must not feed
-- back into its own recomputation, and true when checking a declaration against the
-- holding as the user sees it. Either bound may be NULL for an open one.
-- See docs/spec/bitemporality.md and docs/adr/0028-cumulative-split-factor-is-an-exact-rational.md.
CREATE OR REPLACE FUNCTION holding_qty_in_basis(
  p_user_id UUID,
  p_broker TEXT,
  p_account TEXT,
  p_instrument_id UUID,
  p_from TIMESTAMPTZ,
  p_before TIMESTAMPTZ,
  p_basis DATE,
  p_include_synthetic BOOLEAN,
  OUT qty NUMERIC,
  OUT posting_count INT,
  OUT inexact_bases INT
) LANGUAGE sql STABLE AS $$
  WITH per_basis AS (
    SELECT t.share_count_basis AS basis, SUM(t.quantity) AS q, COUNT(*)::int AS n
    FROM txs t
    WHERE t.user_id = p_user_id
      AND t.broker = p_broker
      AND t.account = p_account
      AND t.instrument_id = p_instrument_id
      AND t.account_type = 'USER'
      AND (p_include_synthetic OR t.synthetic_purpose IS NULL)
      AND (p_from IS NULL OR t.timestamp >= p_from)
      AND (p_before IS NULL OR t.timestamp < p_before)
    GROUP BY t.share_count_basis
  )
  SELECT COALESCE(SUM(p.q * fb.num * fd.den / (fb.den * fd.num)), 0),
         COALESCE(SUM(p.n), 0)::int,
         COUNT(*) FILTER (WHERE fb.num * fd.den <> fb.den * fd.num)::int
  FROM per_basis p,
       LATERAL split_factor_at(p_instrument_id, p.basis) fb,
       LATERAL split_factor_at(p_instrument_id, p_basis) fd;
$$;

-- Holding declarations: user-provided statement of known holding quantity at a date.
-- Holdings are computed aggregates identified by (broker, account, instrument_id).
--
-- share_count_basis is the date at which the share count declared_qty is denominated
-- in was current. It defaults to as_of_date: a user reading a quantity off a
-- statement of that date means the share count current then. A user reading today's
-- holdings screen means today's, and says so. Without the declaration stating which,
-- it and the postings it is reconciled against can be in different units, and a
-- correct portfolio disagrees with itself by the split factor.
-- See docs/spec/bitemporality.md.
--
-- The unique key carries as_of_date, so a holding may hold several declarations at
-- different dates. The earliest is the pad: it generates the INITIALIZE transaction
-- that makes the declared quantity true, and it is true by construction, so it can
-- never catch an error. The later ones are assertions -- statements the computed
-- holding is checked against, which is where the safety comes from. The
-- discriminator is not stored: deriving it from MIN(as_of_date) leaves nothing to
-- drift out of step with the rows.
-- See docs/spec/fixed-point.md and docs/adr/0011-synthetic-initialize-transactions.md.
CREATE TABLE holding_declarations (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  broker            TEXT NOT NULL,
  account           TEXT NOT NULL,
  instrument_id     UUID NOT NULL REFERENCES instruments(id),
  declared_qty      NUMERIC NOT NULL,
  as_of_date        DATE NOT NULL,
  share_count_basis DATE NOT NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, broker, account, instrument_id, as_of_date)
);

-- The default lives here rather than in the application so that every path into the
-- table -- the API, an integration test, a psql session -- lands on the same
-- denomination. It is an insert-time decision: once a declaration states its basis,
-- moving as_of_date does not restate it.
CREATE OR REPLACE FUNCTION default_declaration_share_count_basis() RETURNS TRIGGER AS $$
BEGIN
  IF NEW.share_count_basis IS NULL THEN
    NEW.share_count_basis := NEW.as_of_date;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_default_declaration_share_count_basis
  BEFORE INSERT ON holding_declarations
  FOR EACH ROW EXECUTE FUNCTION default_declaration_share_count_basis();

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

-- qty_is_zero is a null-safe test for a closed position, applied to a sum of
-- split_adjusted_quantity. Raw quantities cannot be summed at all: each is
-- denominated in its own row's share_count_basis, so a buy recorded before a
-- split and a sell recorded after it are in different units and adding them
-- scales the total by part of the split factor. The split-adjusted column is
-- already converted to today's share count, so the sum is in one denomination.
--
-- What that costs is exactness. The column declares a rounding scale of 12, so a
-- row whose conversion did not land on a representable decimal rounds once, and a
-- genuinely closed position sums to something near zero rather than to zero.
-- inexact_postings is how many contributing rows may carry such a rounding, and
-- one unit in the last place each is the most the sum can differ from a true
-- zero. Callers count the rows whose adjusted quantity differs from their raw
-- one: a row that converted by 1/1 cannot have rounded, and counting the rest
-- overstates by the ones that converted exactly, which is the safe direction for
-- a bound. Passing zero is an exact test.
--
-- The NULL branch is what the valuation day grid depends on: a holding has no
-- position on a date before its instrument's first tx, and that reads as closed
-- rather than as unknown.
--
-- See docs/spec/bitemporality.md, adr/0026-exact-decimals-bounded-by-closure.md
-- and adr/0028-cumulative-split-factor-is-an-exact-rational.md.
CREATE FUNCTION qty_is_zero(q numeric, inexact_postings int) RETURNS boolean
    LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT q IS NULL OR abs(q) <= COALESCE(inexact_postings, 0) * 1e-12
$$;
