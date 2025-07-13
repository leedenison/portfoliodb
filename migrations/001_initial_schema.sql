-- Enable TimescaleDB extension
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Create instruments table (regular PostgreSQL table)
CREATE TABLE instruments (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL, -- STK, MF, BOND, etc.
    currency TEXT -- ISO 4217 currency code (e.g., USD, EUR, GBP), now nullable
);

-- Create symbols table (one-to-many relationship)
CREATE TABLE symbols (
    id BIGSERIAL PRIMARY KEY,
    instrument_id BIGINT NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    domain TEXT NOT NULL,
    symbol TEXT NOT NULL,
    exchange TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    UNIQUE(instrument_id, domain, symbol, exchange, description)
);

-- Create derivatives table (for options, futures, etc.)
CREATE TABLE derivatives (
    id BIGSERIAL PRIMARY KEY,
    instrument_id BIGINT NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    underlying_instrument_id BIGINT NOT NULL REFERENCES instruments(id),
    expiration_date TIMESTAMPTZ NOT NULL,
    put_call TEXT NOT NULL CHECK (put_call IN ('PUT', 'CALL')),
    strike_price DOUBLE PRECISION NOT NULL,
    multiplier DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    UNIQUE(instrument_id)
);

-- Create transactions hypertable (TimescaleDB timeseries)
-- Note: For TimescaleDB hypertables with primary keys, the partitioning column must be included
CREATE TABLE transactions (
    id BIGSERIAL,
    account_id TEXT NOT NULL,
    instrument_id BIGINT NOT NULL REFERENCES instruments(id),
    units DOUBLE PRECISION NOT NULL,
    unit_price DOUBLE PRECISION,
    currency TEXT NOT NULL, -- ISO 4217 currency code (e.g., USD, EUR, GBP)
    trade_date TIMESTAMPTZ NOT NULL,
    settled_date TIMESTAMPTZ,
    tx_type TEXT NOT NULL, -- BUY, SELL, DIVIDEND, etc.
    PRIMARY KEY (id, trade_date)
);

-- Create prices hypertable (TimescaleDB timeseries)
-- Note: For TimescaleDB hypertables with primary keys, the partitioning column must be included
CREATE TABLE prices (
    id BIGSERIAL,
    instrument_id BIGINT NOT NULL REFERENCES instruments(id),
    price DOUBLE PRECISION NOT NULL,
    price_date TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (id, price_date)
);

-- Convert to hypertables with 1-day chunking
SELECT create_hypertable('transactions', 'trade_date', chunk_time_interval => INTERVAL '1 day');
SELECT create_hypertable('prices', 'price_date', chunk_time_interval => INTERVAL '1 day');

-- Create indexes for performance
CREATE INDEX idx_transactions_account_date ON transactions(account_id, trade_date);
CREATE INDEX idx_transactions_instrument_date ON transactions(instrument_id, trade_date);
CREATE INDEX idx_transactions_settled_date ON transactions(settled_date);
CREATE INDEX idx_prices_instrument_date ON prices(instrument_id, price_date);
CREATE INDEX idx_symbols_lookup ON symbols(domain, symbol, exchange, description);
CREATE INDEX idx_derivatives_underlying ON derivatives(underlying_instrument_id);

-- Create unique constraints to prevent concurrent updates
CREATE UNIQUE INDEX idx_transactions_account_instrument_trade_date ON transactions(account_id, instrument_id, trade_date);