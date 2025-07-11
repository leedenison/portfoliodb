-- Enable TimescaleDB extension
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Create instruments table (regular PostgreSQL table)
CREATE TABLE instruments (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL, -- STK, MF, BOND, etc.
    currency VARCHAR(3) -- ISO 4217 currency code (e.g., USD, EUR, GBP), now nullable
);

-- Create symbols table (one-to-many relationship)
CREATE TABLE symbols (
    id BIGSERIAL PRIMARY KEY,
    instrument_id BIGINT NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    domain VARCHAR(100) NOT NULL,
    symbol VARCHAR(100) NOT NULL,
    exchange VARCHAR(100) NOT NULL,
    UNIQUE(instrument_id, domain, symbol, exchange)
);

-- Create derivatives table (for options, futures, etc.)
CREATE TABLE derivatives (
    id BIGSERIAL PRIMARY KEY,
    instrument_id BIGINT NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    underlying_instrument_id BIGINT NOT NULL REFERENCES instruments(id),
    expiration_date TIMESTAMPTZ NOT NULL,
    put_call VARCHAR(4) NOT NULL CHECK (put_call IN ('PUT', 'CALL')),
    strike_price DOUBLE PRECISION NOT NULL,
    multiplier DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    UNIQUE(instrument_id)
);

-- Create transactions hypertable (TimescaleDB timeseries)
CREATE TABLE transactions (
    id BIGSERIAL PRIMARY KEY,
    account_id VARCHAR(255) NOT NULL,
    instrument_id BIGINT NOT NULL REFERENCES instruments(id),
    units DOUBLE PRECISION NOT NULL,
    unit_price DOUBLE PRECISION,
    currency VARCHAR(3) NOT NULL, -- ISO 4217 currency code (e.g., USD, EUR, GBP)
    trade_date TIMESTAMPTZ NOT NULL,
    settled_date TIMESTAMPTZ,
    tx_type VARCHAR(20) NOT NULL -- BUY, SELL, DIVIDEND, etc.
);

-- Create prices hypertable (TimescaleDB timeseries)
CREATE TABLE prices (
    id BIGSERIAL PRIMARY KEY,
    instrument_id BIGINT NOT NULL REFERENCES instruments(id),
    price DOUBLE PRECISION NOT NULL,
    price_date TIMESTAMPTZ NOT NULL
);

-- Convert to hypertables with 1-day chunking
SELECT create_hypertable('transactions', 'trade_date', chunk_time_interval => INTERVAL '1 day');
SELECT create_hypertable('prices', 'price_date', chunk_time_interval => INTERVAL '1 day');

-- Create indexes for performance
CREATE INDEX idx_transactions_account_date ON transactions(account_id, trade_date);
CREATE INDEX idx_transactions_instrument_date ON transactions(instrument_id, trade_date);
CREATE INDEX idx_transactions_settled_date ON transactions(settled_date);
CREATE INDEX idx_prices_instrument_date ON prices(instrument_id, price_date);
DROP INDEX IF EXISTS idx_symbols_lookup;
CREATE INDEX idx_symbols_lookup ON symbols(domain, symbol, exchange);
CREATE INDEX idx_derivatives_underlying ON derivatives(underlying_instrument_id);

-- Create unique constraints to prevent concurrent updates
CREATE UNIQUE INDEX idx_transactions_account_instrument_trade_date ON transactions(account_id, instrument_id, trade_date);
CREATE UNIQUE INDEX idx_prices_instrument_date ON prices(instrument_id, price_date);