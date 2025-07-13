-- Enable TimescaleDB extension
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Create instruments table (regular PostgreSQL table)
CREATE TABLE instruments (
    dbid BIGSERIAL PRIMARY KEY,
    type TEXT NOT NULL, -- STK, MF, BOND, etc.
    currency TEXT -- ISO 4217 currency code (e.g., USD, EUR, GBP), now nullable
);

-- Create identifiers table (one-to-many relationship)
CREATE TABLE identifiers (
    dbid BIGSERIAL PRIMARY KEY,
    instrument_dbid BIGINT NOT NULL REFERENCES instruments(dbid) ON DELETE CASCADE,
    id TEXT NOT NULL,
    domain TEXT NOT NULL,
    symbol TEXT NOT NULL,
    exchange TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    UNIQUE(instrument_dbid, id, domain, symbol, exchange, description)
);

-- Create derivatives table (for options, futures, etc.)
CREATE TABLE derivatives (
    dbid BIGSERIAL PRIMARY KEY,
    instrument_dbid BIGINT NOT NULL REFERENCES instruments(dbid) ON DELETE CASCADE,
    underlying_dbid BIGINT NOT NULL REFERENCES instruments(dbid),
    expiration_date TIMESTAMPTZ NOT NULL,
    put_call TEXT NOT NULL CHECK (put_call IN ('PUT', 'CALL')),
    strike_price DOUBLE PRECISION NOT NULL,
    multiplier DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    UNIQUE(instrument_dbid)
);

-- Create transactions hypertable (TimescaleDB timeseries)
-- Note: For TimescaleDB hypertables with primary keys, the partitioning column must be included
CREATE TABLE transactions (
    dbid BIGSERIAL,
    account_id TEXT NOT NULL,
    instrument_dbid BIGINT NOT NULL REFERENCES instruments(dbid),
    units DOUBLE PRECISION NOT NULL,
    unit_price DOUBLE PRECISION,
    currency TEXT NOT NULL, -- ISO 4217 currency code (e.g., USD, EUR, GBP)
    trade_date TIMESTAMPTZ NOT NULL,
    settled_date TIMESTAMPTZ,
    tx_type TEXT NOT NULL, -- BUY, SELL, DIVIDEND, etc.
    PRIMARY KEY (dbid, trade_date)
);

-- Create prices hypertable (TimescaleDB timeseries)
-- Note: For TimescaleDB hypertables with primary keys, the partitioning column must be included
CREATE TABLE prices (
    dbid BIGSERIAL,
    instrument_dbid BIGINT NOT NULL REFERENCES instruments(dbid),
    price DOUBLE PRECISION NOT NULL,
    price_date TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (dbid, price_date)
);

-- Convert to hypertables with 1-day chunking
SELECT create_hypertable('transactions', 'trade_date', chunk_time_interval => INTERVAL '1 day');
SELECT create_hypertable('prices', 'price_date', chunk_time_interval => INTERVAL '1 day');

-- Create indexes for performance
CREATE INDEX idx_transactions_account_date ON transactions(account_id, trade_date);
CREATE INDEX idx_transactions_instrument_date ON transactions(instrument_dbid, trade_date);
CREATE INDEX idx_transactions_settled_date ON transactions(settled_date);
CREATE INDEX idx_prices_instrument_date ON prices(instrument_dbid, price_date);
CREATE INDEX idx_identifiers_lookup ON identifiers(domain, symbol, exchange, description);
CREATE INDEX idx_derivatives_underlying ON derivatives(underlying_dbid);

-- Create unique constraints to prevent concurrent updates
CREATE UNIQUE INDEX idx_transactions_account_instrument_trade_date ON transactions(account_id, instrument_dbid, trade_date);