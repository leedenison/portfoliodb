
CREATE TABLE retry_symbol_description (
  dbid BIGSERIAL PRIMARY KEY,
  symbol_description_dbid BIGINT REFERENCES symbol_descriptions(dbid) ON DELETE CASCADE,
  domain TEXT,
  exchange TEXT,
  symbol TEXT,
  currency TEXT,
  retry_count INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_attempted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE retry_symbol (
  dbid BIGSERIAL PRIMARY KEY,
  symbol_dbid BIGINT REFERENCES symbols(dbid) ON DELETE CASCADE,
  retry_count INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_attempted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);