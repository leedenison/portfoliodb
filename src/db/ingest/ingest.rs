use anyhow::Result;
use sea_orm::{ActiveModelTrait, Set, ColumnTrait, EntityTrait, QueryFilter};
use chrono::{DateTime, Utc};
use crate::db::ingest::models::{BatchActiveModel, StagingTxActiveModel, Batches};
use crate::db::ingest::models::{batches, staging_txs};
use crate::db::DatabaseManager;
use crate::db::ingest::api::IngestStore;
use crate::portfolio_db::{Tx};

#[async_trait::async_trait]
impl<E> IngestStore for DatabaseManager<E>
where
    E: sea_orm::ConnectionTrait + sea_orm::TransactionTrait + Send + Sync,
{
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
    async fn create_batch(
        &self,
        user_dbid: i64,
        batch_type: &str,
        broker_key: &str,
        period_start: DateTime<Utc>,
        period_end: DateTime<Utc>,
    ) -> Result<i64> {
        let batch = BatchActiveModel {
            user_dbid: Set(user_dbid),
            batch_type: Set(batch_type.to_string()),
            broker_key: Set(broker_key.to_string()),
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

        let result = batch.insert(self.exec()).await?;
        Ok(result.batch_dbid)
    }

    /// Bulk inserts Tx data into staging_txs table using SeaORM ActiveModel.
    /// 
    /// # Arguments
    /// * `batch_dbid` - The batch database ID to associate with the transactions
    /// * `transactions` - Iterator over Tx protobuf types
    ///
    /// # Returns
    /// * `Ok(record_count)` - Number of records successfully inserted
    /// * `Err` if a database error occurs
    async fn stage_txs(
        &self,
        batch_dbid: i64,
        transactions: Box<dyn Iterator<Item = Tx> + Send>,
    ) -> Result<usize> {
        let mut record_count = 0;
        let mut active_models = Vec::new();
        
        for tx in transactions {
            record_count += 1;
            let active_model = StagingTxActiveModel::from(tx).with_batch_dbid(batch_dbid);
            active_models.push(active_model);
        }
        
        if record_count == 0 {
            return Ok(0);
        }

        staging_txs::Entity::insert_many(active_models)
            .exec(self.exec())
            .await?;
        
        Ok(record_count)
    }

    /// Updates the total_records field of a batch.
    /// 
    /// # Arguments
    /// * `batch_dbid` - The batch database ID to update
    /// * `total_records` - The total number of records in the batch
    ///
    /// # Returns
    /// * `Ok(())` if the batch was updated successfully
    /// * `Err` if a database error occurs
    async fn update_batch_total_records(
        &self,
        batch_dbid: i64,
        total_records: i32,
    ) -> Result<()> {
        let result = Batches::update_many()
            .col_expr(batches::Column::TotalRecords, total_records.into())
            .filter(batches::Column::BatchDbid.eq(batch_dbid))
            .exec(self.exec())
            .await?;

        if result.rows_affected == 0 {
            return Err(anyhow::anyhow!("Batch with id {} not found", batch_dbid));
        }

        Ok(())
    }

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
    async fn update_batch_status<'a>(
        &'a self,
        batch_dbid: i64,
        status: &'a str,
        error_message: Option<&'a str>,
    ) -> Result<()> {
        let mut update_query = Batches::update_many()
            .col_expr(batches::Column::Status, status.into())
            .filter(batches::Column::BatchDbid.eq(batch_dbid));

        if let Some(msg) = error_message {
            update_query = update_query.col_expr(batches::Column::ErrorMessage, msg.into());
        }

        if status == "COMPLETED" || status == "FAILED" {
            update_query = update_query.col_expr(batches::Column::ProcessedAt, Utc::now().into());
        }

        let result = update_query.exec(self.exec()).await?;

        if result.rows_affected == 0 {
            return Err(anyhow::anyhow!("Batch with id {} not found", batch_dbid));
        }

        Ok(())
    }
} 