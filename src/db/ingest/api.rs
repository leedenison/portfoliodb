use anyhow::Result;
use sea_orm::DatabaseTransaction;
use chrono::{DateTime, Utc};
use crate::portfolio_db::{Tx, Price};

/// Trait defining the ingest operations for PortfolioDB.
/// This trait abstracts the ingest operations and allows for easier testing
/// by enabling mock implementations.
#[async_trait::async_trait]
pub trait IngestStore {
    /// Creates a new batch for ingestion and returns the batch_dbid.
    /// 
    /// # Arguments
    /// * `user_dbid` - Optional user database ID
    /// * `batch_type` - Type of batch ('txs_timeseries' or 'prices_timeseries')
    /// * `period_start` - Start of the period for this batch
    /// * `period_end` - End of the period for this batch
    /// * `txn` - Optional database transaction. If None, a new transaction will be created and committed.
    ///
    /// # Returns
    /// * `Ok(batch_dbid)` if the batch was created successfully
    /// * `Err` if a database error occurs
    async fn create_batch(
        &self,
        user_dbid: Option<i64>,
        batch_type: &str,
        period_start: DateTime<Utc>,
        period_end: DateTime<Utc>,
        txn: Option<&DatabaseTransaction>,
    ) -> Result<i64>;

    /// Bulk inserts Tx data into staging_txs table using SeaORM ActiveModel.
    /// 
    /// # Arguments
    /// * `transactions` - Iterator over Tx protobuf types
    /// * `batch_dbid` - The batch database ID to associate with the transactions
    /// * `txn` - Optional database transaction. If None, a new transaction will be created and committed.
    ///
    /// # Returns
    /// * `Ok(record_count)` - Number of records successfully inserted
    /// * `Err` if a database error occurs
    async fn stage_txs(
        &self,
        batch_dbid: i64,
        transactions: Box<dyn Iterator<Item = Tx> + Send>,
        txn: Option<&DatabaseTransaction>,
    ) -> Result<usize>;

    /// Bulk inserts Price data into staging_prices table using SeaORM ActiveModel.
    /// 
    /// # Arguments
    /// * `prices` - Iterator over Price protobuf types
    /// * `batch_dbid` - The batch database ID to associate with the prices
    /// * `txn` - Optional database transaction. If None, a new transaction will be created and committed.
    ///
    /// # Returns
    /// * `Ok(record_count)` - Number of records successfully inserted
    /// * `Err` if a database error occurs
    async fn stage_prices(
        &self,
        batch_dbid: i64,
        prices: Box<dyn Iterator<Item = Price> + Send>,
        txn: Option<&DatabaseTransaction>,
    ) -> Result<usize>;

    /// Updates the total_records field of a batch.
    /// 
    /// # Arguments
    /// * `batch_dbid` - The batch database ID to update
    /// * `total_records` - The total number of records in the batch
    /// * `txn` - Optional database transaction. If None, a new transaction will be created and committed.
    ///
    /// # Returns
    /// * `Ok(())` if the batch was updated successfully
    /// * `Err` if a database error occurs
    async fn update_batch_total_records(
        &self,
        batch_dbid: i64,
        total_records: i32,
        txn: Option<&DatabaseTransaction>,
    ) -> Result<()>;

    /// Updates the status and error_message fields of a batch.
    /// 
    /// # Arguments
    /// * `batch_dbid` - The batch database ID to update
    /// * `status` - The new status for the batch
    /// * `error_message` - Optional error message
    /// * `txn` - Optional database transaction. If None, a new transaction will be created and committed.
    ///
    /// # Returns
    /// * `Ok(())` if the batch was updated successfully
    /// * `Err` if a database error occurs
    async fn update_batch_status(
        &self,
        batch_dbid: i64,
        status: &str,
        error_message: Option<&str>,
        txn: Option<&DatabaseTransaction>,
    ) -> Result<()>;
} 