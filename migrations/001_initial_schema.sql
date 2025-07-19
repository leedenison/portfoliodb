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
    dbid BIGSERIAL PRIMARY KEY,
    key TEXT NOT NULL,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(key)
);

-- Create instruments table (regular PostgreSQL table)
CREATE TABLE instruments (
    dbid BIGSERIAL PRIMARY KEY,
    type TEXT NOT NULL, -- STK, MF, BOND, etc.
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Stores broker descriptions used to identify instruments
    dbid BIGSERIAL PRIMARY KEY,
    -- instrument_dbid: one-to-many
    instrument_dbid BIGINT NOT NULL REFERENCES instruments(dbid) ON DELETE CASCADE,
    -- user_dbid: one-to-many
    user_dbid BIGINT NOT NULL,
    -- broker_dbid: one-to-many
    broker_dbid BIGINT NOT NULL REFERENCES brokers(dbid) ON DELETE CASCADE,
    description TEXT NOT NULL,
    canonical BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(broker_dbid, description),
    UNIQUE(user_dbid, broker_dbid, description)
);

-- Stores ISIN, CUSIP, SEDOL, etc identifiers for instruments
    dbid BIGSERIAL PRIMARY KEY,
    -- instrument_dbid: one-to-many
    instrument_dbid BIGINT NOT NULL REFERENCES instruments(dbid) ON DELETE CASCADE,
    -- user_dbid: one-to-many
    user_dbid BIGINT NOT NULL,
    domain TEXT NOT NULL,
    id TEXT NOT NULL,
    canonical BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(domain, id),
    UNIQUE(user_dbid, domain, id)
);

-- Stores (exchange, symbol, currency) triplets used to identify instruments (canonical and user-contributed)
CREATE TABLE instrument_symbols (
    dbid BIGSERIAL PRIMARY KEY,
    -- instrument_dbid: one-to-many
    instrument_dbid BIGINT NOT NULL REFERENCES instruments(dbid) ON DELETE CASCADE,
    -- user_dbid: one-to-many
    user_dbid BIGINT NOT NULL,
    domain TEXT NOT NULL,
    exchange TEXT NOT NULL,
    symbol TEXT NOT NULL,
    currency TEXT NOT NULL, -- ISO 4217 currency code
    canonical BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(domain, exchange, symbol),
    UNIQUE(user_dbid, domain, exchange, symbol)
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
    -- instrument_dbid: one-to-many
    instrument_dbid BIGINT NOT NULL REFERENCES instruments(dbid),
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
    -- instrument_dbid: one-to-many
    instrument_dbid BIGINT NOT NULL REFERENCES instruments(dbid) ON DELETE CASCADE,
    price DOUBLE PRECISION NOT NULL,
    price_date TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (dbid, price_date)
);

-- Convert to hypertables with 1-day chunking
SELECT create_hypertable('transactions', 'trade_date', chunk_time_interval => INTERVAL '1 day');
SELECT create_hypertable('prices', 'price_date', chunk_time_interval => INTERVAL '1 day');

-- Create indexes for performance
CREATE INDEX idx_transactions_account_date ON transactions(user_dbid, account_id, instrument_dbid, trade_date);
CREATE INDEX idx_prices_instrument_date ON prices(instrument_dbid, price_date);