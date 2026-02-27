-- Local instrument reference for server/plugins/local/identifier.
-- Canonical instrument data lives once; identifiers (broker descriptions, ISIN, CUSIP, SYMBOL, etc.) reference it.
-- New identifier types can be added without schema changes. Populated manually (e.g. COPY from CSV).
-- Operator applies this migration when creating/updating the datamodel.

-- One row per logical instrument (canonical security-master data).
CREATE TABLE IF NOT EXISTS local_instruments (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  asset_class TEXT,
  exchange    TEXT,
  currency    TEXT,
  name        TEXT
);

-- Many identifiers per instrument. (identifier_type, value) is unique globally for lookup.
-- Broker descriptions: identifier_type = broker name ('IBKR', 'SCHB'), value = full instrument_description.
CREATE TABLE IF NOT EXISTS local_instrument_identifiers (
  instrument_id   UUID NOT NULL REFERENCES local_instruments (id) ON DELETE CASCADE,
  identifier_type TEXT NOT NULL,
  value           TEXT NOT NULL,
  PRIMARY KEY (instrument_id, identifier_type, value),
  UNIQUE (identifier_type, value)
);

CREATE INDEX IF NOT EXISTS idx_local_instrument_identifiers_lookup
  ON local_instrument_identifiers (identifier_type, value);
