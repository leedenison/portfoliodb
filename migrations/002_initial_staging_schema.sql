-- Staging metadata table to track ingestion batches
CREATE TABLE staging_batches (
    batch_dbid BIGSERIAL PRIMARY KEY,
    user_dbid BIGINT,
    batch_type TEXT NOT NULL CHECK (batch_type IN ('txs_timeseries', 'prices_timeseries')),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    total_records INTEGER NOT NULL DEFAULT 0,
    processed_records INTEGER NOT NULL DEFAULT 0,
    error_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ,
    error_message TEXT
);

-- Removed staging_references table

CREATE TABLE staging_txs (
    batch_dbid BIGINT NOT NULL REFERENCES staging_batches(batch_dbid) ON DELETE CASCADE,
    broker_key TEXT,
    description TEXT,
    domain TEXT,
    exchange TEXT,
    symbol TEXT,
    symbol_currency TEXT,
    currency TEXT,
    account_id TEXT,
    units DOUBLE PRECISION NOT NULL,
    unit_price DOUBLE PRECISION,
    trade_date TIMESTAMPTZ NOT NULL,
    settled_date TIMESTAMPTZ,
    tx_type TEXT NOT NULL
);

CREATE TABLE staging_prices (
    batch_dbid BIGINT NOT NULL REFERENCES staging_batches(batch_dbid) ON DELETE CASCADE,
    domain TEXT,
    exchange TEXT,
    symbol TEXT,
    currency TEXT,
    price DOUBLE PRECISION NOT NULL,
    date_as_of TIMESTAMPTZ NOT NULL
);

-- Create a function to clean up old staging batches
CREATE OR REPLACE FUNCTION delete_stale_staging_batches()
RETURNS void AS $$
BEGIN
    -- Delete staging_batches records older than 90 days
    -- This will cascade to related tables due to ON DELETE CASCADE
    DELETE FROM staging_batches 
    WHERE created_at < NOW() - INTERVAL '90 days';
    
    RAISE NOTICE 'Cleaned up staging_batches older than 90 days at %', NOW();
END;
$$ LANGUAGE plpgsql;

-- Schedule the cleanup job to run daily at 2 AM
SELECT cron.schedule(
    'cleanup-staging-batches',
    '0 2 * * *',
    'SELECT delete_stale_staging_batches();'
);
