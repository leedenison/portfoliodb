use crate::db::ingest::models::StagingIdentifier;
use crate::portfolio_db::{Instrument, Tx};
use anyhow::Result;
use chrono::{DateTime, Utc};

pub trait IngestStore {
    /// Creates a new batch for ingestion and returns the batch_dbid.
    ///
    /// # Arguments
    /// * `user_dbid` - Optional user database ID
    /// * `batch_type` - Type of batch ('txs_timeseries' or 'prices_timeseries')
    /// * `broker_key` - The broker key for this batch
    /// * `period_start` - Start of the period for this batch
    /// * `period_end` - End of the period for this batch
    ///
    /// # Returns
    /// * `Ok(batch_dbid)` if the batch was created successfully
    /// * `Err` if a database error occurs
    fn create_batch(
        &self,
        user_dbid: i64,
        batch_type: &str,
        broker_key: &str,
        period_start: DateTime<Utc>,
        period_end: DateTime<Utc>,
    ) -> impl Future<Output = Result<i64>> + Send;

    /// Updates the total_records field of a batch.
    ///
    /// # Arguments
    /// * `batch_dbid` - The batch database ID to update
    /// * `total_records` - The total number of records in the batch
    ///
    /// # Returns
    /// * `Ok(())` if the batch was updated successfully
    /// * `Err` if a database error occurs
    fn update_batch_total_records(
        &self,
        batch_dbid: i64,
        total_records: i32,
    ) -> impl Future<Output = Result<()>> + Send;

    /// Updates the status and error_message fields of a batch.
    ///
    /// # Arguments
    /// * `batch_dbid` - The batch database ID to update
    /// * `status` - The new status for the batch
    /// * `error_message` - Optional error message
    ///
    /// # Returns
    /// * `Ok(())` if the batch was updated successfully
    /// * `Err` if a database error occurs
    fn update_batch_status<'a>(
        &'a self,
        batch_dbid: i64,
        status: &'a str,
        error_message: Option<&'a str>,
    ) -> impl Future<Output = Result<()>> + Send;

    /// Bulk inserts Tx data into staging_txs table using SeaORM ActiveModel.
    ///
    /// # Arguments
    /// * `batch_dbid` - The batch database ID to associate with the transactions
    /// * `transactions` - Iterator over Tx protobuf types
    ///
    /// # Returns
    /// * `Ok(record_count)` - Number of records successfully inserted
    /// * `Err` if a database error occurs
    fn stage_txs(
        &self,
        batch_dbid: i64,
        transactions: Box<dyn Iterator<Item = Tx> + Send>,
    ) -> impl Future<Output = Result<usize>> + Send;

    /// Bulk inserts Instrument data into staging_instruments and staging_identifiers tables.
    ///
    /// # Arguments
    /// * `batch_dbid` - The batch database ID to associate with the instruments
    /// * `instruments` - Iterator over Instrument protobuf types
    ///
    /// # Returns
    /// * `Ok(record_count)` - Total number of records (identifiers + instruments) successfully inserted
    /// * `Err` if a database error occurs
    fn stage_instruments(
        &self,
        batch_dbid: i64,
        source: String,
        instruments: Box<dyn Iterator<Item = Instrument> + Send>,
    ) -> impl Future<Output = Result<usize>> + Send;

    /// Returns all unresolved identifiers for a batch.
    ///
    /// # Arguments
    /// * `batch_dbid` - The batch database ID to get unresolved identifiers for
    ///
    /// # Returns
    /// * `Ok(identifiers)` - Vector of unresolved identifiers
    /// * `Err` if a database error occurs
    fn unresolved_identifiers(
        &self,
        batch_dbid: i64,
    ) -> impl Future<Output = Result<Vec<StagingIdentifier>>> + Send;
}
