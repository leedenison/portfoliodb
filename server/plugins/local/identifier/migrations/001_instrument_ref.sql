-- Local instrument reference for server/plugins/local/identifier.
-- Canonical instrument data lives once; identifiers (broker descriptions, ISIN, CUSIP, SYMBOL, etc.) reference it.
-- New identifier types can be added without schema changes. Populated manually (e.g. COPY from CSV).
-- Operator applies this migration when creating/updating the datamodel.

-- One row per logical instrument (canonical security-master data).
-- asset_class: controlled vocabulary. OPTION and FUTURE require underlying_id.
CREATE TABLE IF NOT EXISTS local_instruments (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  asset_class  TEXT CHECK (asset_class IS NULL OR asset_class IN ('EQUITY','ETF','MF','CASH','FIXED_INCOME','OPTION','FUTURE')),
  exchange     TEXT,
  currency     TEXT,
  name         TEXT,
  underlying_id UUID REFERENCES local_instruments (id),
  valid_from   DATE,
  valid_to     DATE,
  CONSTRAINT chk_local_underlying_required CHECK (
    (asset_class IN ('OPTION','FUTURE') AND underlying_id IS NOT NULL)
    OR (asset_class IS NULL OR asset_class NOT IN ('OPTION','FUTURE'))
  )
);

CREATE INDEX IF NOT EXISTS idx_local_instruments_underlying_id ON local_instruments (underlying_id);

-- Many identifiers per instrument. (identifier_type, value) is unique globally for lookup.
-- canonical = false only for broker-description identifiers; canonical = true for standard identifiers (ISIN, CUSIP, etc.).
-- Broker descriptions: identifier_type = broker name ('IBKR', 'SCHB'), value = full instrument_description.
CREATE TABLE IF NOT EXISTS local_instrument_identifiers (
  instrument_id   UUID NOT NULL REFERENCES local_instruments (id) ON DELETE CASCADE,
  identifier_type TEXT NOT NULL,
  value           TEXT NOT NULL,
  canonical       BOOLEAN NOT NULL DEFAULT true,
  PRIMARY KEY (instrument_id, identifier_type, value),
  UNIQUE (identifier_type, value)
);

CREATE INDEX IF NOT EXISTS idx_local_instrument_identifiers_lookup
  ON local_instrument_identifiers (identifier_type, value);
