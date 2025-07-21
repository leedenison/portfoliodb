-- Enable TimescaleDB extension
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Create users table
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

-- Create instruments table (regular PostgreSQL table)
CREATE TABLE instruments (
    dbid BIGSERIAL PRIMARY KEY,
    type TEXT NOT NULL, -- STK, MF, BOND, etc.
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Stores ISIN, CUSIP, SEDOL, etc identifiers for instruments
CREATE TABLE instrument_identifiers (
    dbid BIGSERIAL PRIMARY KEY,
    -- instrument_dbid: one-to-many
    instrument_dbid BIGINT NOT NULL REFERENCES instruments(dbid) ON DELETE CASCADE,
    domain TEXT NOT NULL,
    id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(domain, id)
);

-- Stores (exchange, symbol, currency) triplets used to identify instruments
CREATE TABLE symbols (
    dbid BIGSERIAL PRIMARY KEY,
    -- instrument_dbid: one-to-many
    instrument_dbid BIGINT NOT NULL REFERENCES instruments(dbid) ON DELETE CASCADE,
    domain TEXT NOT NULL,
    exchange TEXT NOT NULL,
    symbol TEXT NOT NULL,
    currency TEXT NOT NULL, -- ISO 4217 currency code
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(domain, exchange, symbol)
);

-- Stores broker descriptions used to identify instruments
CREATE TABLE symbol_descriptions (
    dbid BIGSERIAL PRIMARY KEY,
    -- symbol_dbid: one-to-many
    symbol_dbid BIGINT REFERENCES symbols(dbid),
    -- user_dbid: one-to-many
    user_dbid BIGINT NOT NULL,
    -- broker_key: one-to-many
    broker_key TEXT NOT NULL REFERENCES brokers(key) ON DELETE CASCADE,
    description TEXT NOT NULL,
    canonical BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(broker_key, description),
    UNIQUE(user_dbid, broker_key, description)
);

-- Create derivatives table (for options, futures, etc.)
CREATE TABLE derivatives (
    dbid BIGSERIAL PRIMARY KEY,
    -- instrument_dbid: one-to-one
    instrument_dbid BIGINT NOT NULL REFERENCES instruments(dbid) ON DELETE CASCADE,
    -- underlying_dbid: one-to-many
    underlying_dbid BIGINT NOT NULL REFERENCES instruments(dbid) ON DELETE CASCADE,
    expiration_date TIMESTAMPTZ NOT NULL,
    put_call TEXT NOT NULL CHECK (put_call IN ('PUT', 'CALL')),
    strike_price DOUBLE PRECISION NOT NULL,
    multiplier DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(instrument_dbid)
);

-- Create transactions hypertable (TimescaleDB timeseries)
-- Note: For TimescaleDB hypertables with primary keys, the partitioning column must be included
CREATE TABLE transactions (
    dbid BIGSERIAL,
    -- user_dbid: one-to-many (no foreign key constraint to allow separable users table)
    user_dbid BIGINT NOT NULL,
    -- symbol_dbid: one-to-many
    symbol_dbid BIGINT REFERENCES symbols(dbid),
    -- symbol_description_dbid: one-to-many
    symbol_description_dbid BIGINT REFERENCES symbol_descriptions(dbid),
    account_id TEXT NOT NULL,
    units DOUBLE PRECISION NOT NULL,
    unit_price DOUBLE PRECISION,
    currency TEXT NOT NULL, -- ISO 4217 currency code (e.g., USD, EUR, GBP)
    trade_date TIMESTAMPTZ NOT NULL,
    settled_date TIMESTAMPTZ,
    tx_type TEXT NOT NULL, -- BUY, SELL, DIVIDEND, etc.
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (dbid, trade_date)
);

-- Create prices hypertable (TimescaleDB timeseries)
-- Note: For TimescaleDB hypertables with primary keys, the partitioning column must be included
CREATE TABLE prices (
    dbid BIGSERIAL,
    -- symbol_dbid: one-to-many
    symbol_dbid BIGINT NOT NULL REFERENCES symbols(dbid) ON DELETE CASCADE,
    price DOUBLE PRECISION NOT NULL,
    date_as_of TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (dbid, date_as_of)
);

-- Convert to hypertables with 1-day chunking
SELECT create_hypertable('transactions', 'trade_date', chunk_time_interval => INTERVAL '1 day');
SELECT create_hypertable('prices', 'date_as_of', chunk_time_interval => INTERVAL '1 day');

-- Create indexes for performance
CREATE INDEX idx_transactions_account_date ON transactions(user_dbid, account_id, symbol_dbid, trade_date);
CREATE INDEX idx_prices_instrument_date ON prices(symbol_dbid, date_as_of);