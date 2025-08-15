-- Enable TimescaleDB extension
CREATE EXTENSION IF NOT EXISTS timescaledb;

CREATE TABLE users (
    dbid BIGSERIAL PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    username TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(email)
);

CREATE TABLE brokers (
    key TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Stores instruments; in the case of securities this stores the specific security line.
CREATE TABLE instruments (
    dbid BIGSERIAL PRIMARY KEY, 
    type TEXT NOT NULL CHECK (type IN ('UNKNOWN', 'STK', 'MF', 'BOND', 'CD', 'OPT', 'FUT', 'MFOPT', 'CASH', 'MMF', 'IET', 'FIXED', 'MISC')),
    status TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'RETIRED', 'MERGED')),
    listing_mic TEXT NOT NULL,
    currency TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE identifiers (
    dbid BIGSERIAL PRIMARY KEY,
    -- instrument_dbid: one-to-many
    instrument_dbid BIGINT REFERENCES instruments(dbid) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    user_dbid BIGINT,
    namespace TEXT NOT NULL, -- Yahoo, Bloomberg, GOOGLEFINANCE, CUSIP, ISIN, SEDOL, BROKER_DESCRIPTION, etc.
    domain TEXT NOT NULL, -- MIC, broker key, etc.
    id TEXT NOT NULL,
    source TEXT NOT NULL, -- 'USER', <DISAMBIGUATOR>
    authoritative BOOLEAN NOT NULL DEFAULT FALSE,
    valid_from TIMESTAMPTZ,
    valid_to TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (namespace, domain, id)
);

CREATE UNIQUE INDEX uq_identifier_nodomain ON identifiers (namespace, id) WHERE domain IS NULL;
CREATE INDEX idx_identifiers_line ON identifiers (instrument_line_dbid);

-- Create derivatives table (for options, futures, etc.)
CREATE TABLE derivatives (
    dbid BIGSERIAL PRIMARY KEY,
    -- instrument_dbid: one-to-one
    instrument_dbid BIGINT NOT NULL REFERENCES instruments(dbid) ON DELETE CASCADE,
    -- underlying_dbid: one-to-many
    underlying_dbid BIGINT NOT NULL REFERENCES instruments(dbid) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('OPTION','FUTURE')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(instrument_dbid, underlying_dbid, type)
);

CREATE TABLE option_derivatives (
    dbid BIGSERIAL PRIMARY KEY,
    -- derivative_dbid: one-to-one
    derivative_dbid BIGINT PRIMARY KEY REFERENCES derivatives(dbid) ON DELETE CASCADE,
    expiration_date TIMESTAMPTZ NOT NULL,
    put_call TEXT NOT NULL CHECK (put_call IN ('PUT','CALL')),
    strike_price DOUBLE PRECISION NOT NULL,
    option_style TEXT CHECK (option_style IN ('UNKNOWN','AMERICAN','EUROPEAN','BERMUDAN'))
);

-- Create transactions hypertable (TimescaleDB timeseries)
-- Note: For TimescaleDB hypertables with primary keys, the partitioning column must be included
CREATE TABLE txs (
    dbid BIGSERIAL NOT NULL,
    -- user_dbid: one-to-many (no foreign key constraint to allow separable users table)
    user_dbid BIGINT NOT NULL,
    -- instrument_dbid: one-to-many
    instrument_dbid BIGINT NOT NULL REFERENCES instruments(dbid) ON DELETE CASCADE,
    account_id TEXT NOT NULL,
    units DOUBLE PRECISION NOT NULL,
    unit_price DOUBLE PRECISION,
    currency TEXT NOT NULL, -- ISO 4217 currency code (e.g., USD, EUR, GBP)
    trade_date TIMESTAMPTZ NOT NULL,
    settled_date TIMESTAMPTZ,
    tx_type TEXT NOT NULL CHECK (tx_type IN ('OTHER', 'BUY', 'SELL', 'DIVIDEND', 'INTEREST', 'REINVEST', 'TRANSFER_IN', 'TRANSFER_OUT')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (dbid, trade_date)
);

-- Create prices hypertable (TimescaleDB timeseries)
-- Note: For TimescaleDB hypertables with primary keys, the partitioning column must be included
CREATE TABLE prices (
    dbid BIGSERIAL NOT NULL,
    -- instrument_dbid: one-to-many
    instrument_dbid BIGINT NOT NULL REFERENCES instruments(dbid) ON DELETE CASCADE,
    price DOUBLE PRECISION NOT NULL,
    date_as_of TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (dbid, date_as_of)
);

-- Convert to hypertables with 1-day chunking
SELECT create_hypertable('transactions', 'trade_date', chunk_time_interval => INTERVAL '1 day');
SELECT create_hypertable('prices', 'date_as_of', chunk_time_interval => INTERVAL '1 day');
