-- Staging metadata table to track ingestion batches
CREATE TABLE staging_batches (
    dbid BIGSERIAL PRIMARY KEY,
    user_dbid BIGINT NOT NULL,
    batch_type TEXT NOT NULL CHECK (batch_type IN ('TXS_TIMESERIES', 'PRICES_TIMESERIES')),
    status TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PROCESSING', 'COMPLETED', 'FAILED')),
    broker_key TEXT NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    total_records INTEGER NOT NULL DEFAULT 0,
    processed_records INTEGER NOT NULL DEFAULT 0,
    error_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ,
    error_message TEXT
);

CREATE TABLE staging_instruments (
    dbid BIGSERIAL PRIMARY KEY,
    batch_dbid BIGINT NOT NULL REFERENCES staging_batches(dbid) ON DELETE CASCADE,
    type TEXT NOT NULL,
    status TEXT NOT NULL,
    listing_mic TEXT NOT NULL,
    currency TEXT NOT NULL,
    -- derivative fields
    underlying_namespace TEXT NOT NULL,
    underlying_domain TEXT NOT NULL,
    underlying_identifier TEXT NOT NULL,
    derivative_type TEXT NOT NULL,
    -- option fields
    option_expiration_date TIMESTAMPTZ,
    option_put_call TEXT NOT NULL,
    option_strike_price DOUBLE PRECISION,
    option_style TEXT NOT NULL
);

CREATE TABLE staging_identifiers (
    dbid BIGSERIAL PRIMARY KEY,
    batch_dbid BIGINT NOT NULL REFERENCES staging_batches(dbid) ON DELETE CASCADE,
    instrument_dbid BIGINT NOT NULL REFERENCES staging_instruments(dbid) ON DELETE CASCADE,
    namespace TEXT NOT NULL,
    domain TEXT NOT NULL,
    identifier TEXT NOT NULL
);

CREATE TABLE staging_txs (
    dbid BIGSERIAL PRIMARY KEY,
    batch_dbid BIGINT NOT NULL REFERENCES staging_batches(dbid) ON DELETE CASCADE,
    instrument_namespace TEXT NOT NULL,
    instrument_domain TEXT NOT NULL,
    instrument_identifier TEXT NOT NULL,
    account_id TEXT NOT NULL,
    currency TEXT NOT NULL,
    units DOUBLE PRECISION NOT NULL,
    unit_price DOUBLE PRECISION,
    trade_date TIMESTAMPTZ NOT NULL,
    settled_date TIMESTAMPTZ,
    tx_type TEXT NOT NULL
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
