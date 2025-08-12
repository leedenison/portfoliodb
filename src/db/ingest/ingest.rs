use anyhow::Result;
use sea_orm::{ActiveModelTrait, Set, ColumnTrait, EntityTrait, QueryFilter};
use chrono::{DateTime, Utc};
use crate::db::ingest::models::{BatchActiveModel, StagingTxActiveModel, StagingPriceActiveModel, Batches};
use crate::db::ingest::models::{batches, staging_txs, staging_prices};
use crate::db::DatabaseManager;
use crate::db::ingest::api::IngestStore;
use crate::portfolio_db::{Tx, Price};
use crate::db::models::{SymbolActiveModel, InstrumentActiveModel};
use crate::db::models;

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
        period_start: DateTime<Utc>,
        period_end: DateTime<Utc>,
    ) -> Result<i64> {
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

    /// Bulk inserts Price data into staging_prices table using SeaORM ActiveModel.
    /// 
    /// # Arguments
    /// * `batch_dbid` - The batch database ID to associate with the prices
    /// * `prices` - Iterator over Price protobuf types
    ///
    /// # Returns
    /// * `Ok(record_count)` - Number of records successfully inserted
    /// * `Err` if a database error occurs
    async fn stage_prices(
        &self,
        batch_dbid: i64,
        prices: Box<dyn Iterator<Item = Price> + Send>,
    ) -> Result<usize> {
        let mut record_count = 0;
        let mut active_models = Vec::new();
        
        for price in prices {
            record_count += 1;
            let active_model = StagingPriceActiveModel::from(price).with_batch_dbid(batch_dbid);
            active_models.push(active_model);
        }
        
        if record_count == 0 {
            return Ok(0);
        }

        staging_prices::Entity::insert_many(active_models)
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

    /// Validates staged transactions and updates batch status if validation fails.
    /// 
    /// # Arguments
    /// * `batch_dbid` - The batch database ID to validate
    ///
    /// # Returns
    /// * `Ok(())` if all transactions are valid
    /// * `Err` if validation fails or a database error occurs
    async fn validate_txs(
        &self,
        batch_dbid: i64,
    ) -> Result<()> {
        let invalid_count = staging_txs::Entity::count_invalid_txs(self.exec(), batch_dbid).await?;
        
        if invalid_count > 0 {
            let invalid_txs = staging_txs::Entity::all_invalid_txs(self.exec(), batch_dbid).await?;
            
            let mut error_lines = Vec::new();
            for tx in invalid_txs {
                error_lines.push(format!(
                    "Transaction ID {}: Description and symbol are incomplete: {}",
                    tx.id,
                    tx.trade_date.format("%Y-%m-%d")
                ));
            }
            
            let error_message = format!(
                "Found {} invalid transactions:\n{}",
                invalid_count,
                error_lines.join("\n")
            );
            
            self.update_batch_status(batch_dbid, "FAILED", Some(&error_message)).await?;
            
            return Err(anyhow::anyhow!("{}", error_message));
        }
        
        Ok(())
    }

    /// Creates new symbols and instruments for the given symbol data.
    /// 
    /// # Arguments
    /// * `new_symbols` - Vector of tuples containing (domain, exchange, symbol, currency, instrument_type)
    /// * `disambiguated` - Whether the symbols are disambiguated
    ///
    /// # Returns
    /// * `Ok(Vec<models::Symbol>)` - Vector of created symbols with dbids filled in
    /// * `Err` if a database error occurs
    async fn create_symbols_and_instruments(
        &self,
        new_symbols: Vec<(String, String, String, String, Option<String>)>,
        disambiguated: bool,
    ) -> Result<Vec<models::Symbol>> {
        let mut created_symbols = Vec::new();
        let tx = self.exec().begin().await?;

        for (domain, exchange, symbol, currency, instrument_type) in new_symbols {
            let instrument = InstrumentActiveModel {
                r#type: Set(instrument_type.unwrap_or("UNKNOWN".to_string())),
                created_at: Set(Utc::now()),
                ..Default::default()
            };
            
            let instrument_result = instrument.insert(&tx).await?;
            
            // Create the symbol linked to the instrument
            let symbol_model = SymbolActiveModel {
                instrument_dbid: Set(instrument_result.dbid),
                domain: Set(domain.clone()),
                exchange: Set(exchange.clone()),
                symbol: Set(symbol.clone()),
                currency: Set(currency.clone()),
                disambiguated: Set(disambiguated),
                created_at: Set(Utc::now()),
                ..Default::default()
            };
            
            let symbol_result = symbol_model.insert(&tx).await?;
            created_symbols.push(symbol_result);
        }

        tx.commit().await?;
        Ok(created_symbols)
    }
} 