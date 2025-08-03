use anyhow::Result;
use sea_orm::{ActiveModelTrait, Set, DatabaseTransaction, ColumnTrait, EntityTrait, QueryFilter};
use chrono::{DateTime, Utc};
use crate::db::ingest::models::{BatchActiveModel, StagingTxActiveModel, StagingPriceActiveModel, Batches};
use crate::db::ingest::models::{batches, staging_txs, staging_prices};
use crate::db::DatabaseManager;
use crate::db::LocalTxn;
use crate::db::ingest::api::IngestStore;
use crate::db::api::DataStore;
use crate::portfolio_db::{Tx, Price};

#[async_trait::async_trait]
impl IngestStore for DatabaseManager {
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
    ) -> Result<i64> {
        let mut local_txn = LocalTxn::new(self.connection(), txn).await?;
        
        let batch = BatchActiveModel {
            user_dbid: Set(user_dbid),
            batch_type: Set(batch_type.to_string()),
            status: Set("PENDING".to_string()),
            period_start: Set(period_start),
            period_end: Set(period_end),
            total_records: Set(0),
            processed_records: Set(0),
            error_count: Set(0),
            created_at: Set(Utc::now()),
            processed_at: Set(None),
            error_message: Set(None),
            ..Default::default()
        };

        let result = batch.insert(local_txn.txn()).await?;
        local_txn.commit_if_owned().await?;
        
        Ok(result.batch_dbid)
    }

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
    ) -> Result<usize> {
        let mut local_txn = LocalTxn::new(self.connection(), txn).await?;

        let mut record_count = 0;
        let mut active_models = Vec::new();
        
        for tx in transactions {
            record_count += 1;
            let active_model = StagingTxActiveModel::from(tx).with_batch_dbid(batch_dbid);
            active_models.push(active_model);
        }
        
        if record_count == 0 {
            local_txn.commit_if_owned().await?;
            return Ok(0);
        }

        staging_txs::Entity::insert_many(active_models)
            .exec(local_txn.txn())
            .await?;
            
        local_txn.commit_if_owned().await?;
        
        Ok(record_count)
    }

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
    ) -> Result<usize> {
        let mut local_txn = LocalTxn::new(self.connection(), txn).await?;

        let mut record_count = 0;
        let mut active_models = Vec::new();
        
        for price in prices {
            record_count += 1;
            let active_model = StagingPriceActiveModel::from(price).with_batch_dbid(batch_dbid);
            active_models.push(active_model);
        }
        
        if record_count == 0 {
            local_txn.commit_if_owned().await?;
            return Ok(0);
        }

        staging_prices::Entity::insert_many(active_models)
            .exec(local_txn.txn())
            .await?;
            
        local_txn.commit_if_owned().await?;
        
        Ok(record_count)
    }

    /// Updates the total record count for a batch.
    /// 
    /// # Arguments
    /// * `batch_dbid` - The batch database ID to update
    /// * `total_records` - The new total record count
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
    ) -> Result<()> {
        let mut local_txn = LocalTxn::new(self.connection(), txn).await?;

        let result = Batches::update_many()
            .col_expr(batches::Column::TotalRecords, total_records.into())
            .filter(batches::Column::BatchDbid.eq(batch_dbid))
            .exec(local_txn.txn())
            .await?;

        if result.rows_affected == 0 {
            return Err(anyhow::anyhow!("Batch with id {} not found", batch_dbid));
        }

        local_txn.commit_if_owned().await?;
        
        Ok(())
    }

    /// Updates the status of a batch.
    /// 
    /// # Arguments
    /// * `batch_dbid` - The batch database ID to update
    /// * `status` - The new status for the batch
    /// * `error_message` - Optional error message to store with the batch
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
    ) -> Result<()> {
        let mut local_txn = LocalTxn::new(self.connection(), txn).await?;

        // Build the update query
        let mut update_query = Batches::update_many()
            .col_expr(batches::Column::Status, status.into())
            .filter(batches::Column::BatchDbid.eq(batch_dbid));

        // Add error_message if provided
        if let Some(msg) = error_message {
            update_query = update_query.col_expr(batches::Column::ErrorMessage, msg.into());
        }

        let result = update_query.exec(local_txn.txn()).await?;

        if result.rows_affected == 0 {
            return Err(anyhow::anyhow!("Batch with id {} not found", batch_dbid));
        }

        local_txn.commit_if_owned().await?;
        
        Ok(())
    }
} 