use anyhow::Result;
use sea_orm::{ActiveModelTrait, Set, DatabaseTransaction, ConnectionTrait};
use sea_query::{InsertStatement, UpdateStatement};
use chrono::{DateTime, Utc};
use crate::models::BatchActiveModel;
use super::database::DatabaseManager;
use super::transaction::LocalTxn;
use crate::portfolio_db::{Tx, Price, SymbolDescription, Symbol};

impl DatabaseManager {
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
    pub async fn create_batch(
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
            status: Set("pending".to_string()),
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

    /// Bulk inserts Tx data into staging_txs table using SeaQuery.
    /// 
    /// # Arguments
    /// * `transactions` - Iterator over Tx protobuf types
    /// * `batch_dbid` - The batch database ID to associate with the transactions
    /// * `txn` - Optional database transaction. If None, a new transaction will be created and committed.
    ///
    /// # Returns
    /// * `Ok(record_count)` - Number of records successfully inserted
    /// * `Err` if a database error occurs
    pub async fn stage_txs(
        &self,
        batch_dbid: i64,
        transactions: impl IntoIterator<Item = Tx>,
        txn: Option<&DatabaseTransaction>,
    ) -> Result<usize> {
        let mut local_txn = LocalTxn::new(self.connection(), txn).await?;

        let mut insert_stmt = InsertStatement::new();
        insert_stmt.into_table("staging_txs");
        insert_stmt.columns([
            "batch_dbid",
            "broker_key",
            "description", 
            "domain",
            "exchange",
            "symbol",
            "symbol_currency",
            "currency",
            "account_id",
            "units",
            "unit_price",
            "trade_date",
            "settled_date",
            "tx_type"
        ]);

        let mut record_count = 0;
        for tx in transactions {
            record_count += 1;
            
            let Tx { 
                description, 
                symbol, 
                account_id, 
                units, 
                unit_price, 
                currency: tx_currency, 
                trade_date, 
                settled_date, 
                tx_type, 
                .. 
            } = tx;
            
            let (broker_key, desc) = match description {
                Some(SymbolDescription { id: _, broker_key, description: desc }) => (broker_key, desc),
                None => (String::new(), String::new()),
            };
            
            let (domain, exchange, sym, sym_currency) = match symbol {
                Some(Symbol { id: _, domain, exchange, symbol: sym, currency }) => (
                    domain,
                    exchange,
                    sym,
                    currency
                ),
                None => (
                    String::new(),
                    String::new(),
                    String::new(),
                    String::new()
                ),
            };
            
            let trade_date = trade_date.as_ref()
                .map(|ts| DateTime::from_timestamp(ts.seconds, ts.nanos as u32).unwrap_or_default())
                .unwrap_or_default();
            
            let settled_date = settled_date.as_ref()
                .map(|ts| DateTime::from_timestamp(ts.seconds, ts.nanos as u32).unwrap_or_default());
            
            let tx_type = format!("{:?}", tx_type);

            insert_stmt.values_panic([
                batch_dbid.into(),
                broker_key.into(),
                desc.into(),
                domain.into(),
                exchange.into(),
                sym.into(),
                sym_currency.into(),
                tx_currency.into(),
                account_id.into(),
                units.into(),
                unit_price.into(),
                trade_date.into(),
                settled_date.into(),
                tx_type.into(),
            ]);
        }
        
        if record_count == 0 {
            local_txn.commit_if_owned().await?;
            return Ok(0);
        }

        let query = insert_stmt.to_string(Self::query_builder());
        local_txn.txn().execute_unprepared(&query).await?;
        local_txn.commit_if_owned().await?;
        
        Ok(record_count)
    }

    /// Bulk inserts Price data into staging_prices table using SeaQuery.
    /// 
    /// # Arguments
    /// * `prices` - Iterator over Price protobuf types
    /// * `batch_dbid` - The batch database ID to associate with the prices
    /// * `txn` - Optional database transaction. If None, a new transaction will be created and committed.
    ///
    /// # Returns
    /// * `Ok(record_count)` - Number of records successfully inserted
    /// * `Err` if a database error occurs
    pub async fn stage_prices(
        &self,
        batch_dbid: i64,
        prices: impl IntoIterator<Item = Price>,
        txn: Option<&DatabaseTransaction>,
    ) -> Result<usize> {
        let mut local_txn = LocalTxn::new(self.connection(), txn).await?;

        let mut insert_stmt = InsertStatement::new();
        insert_stmt.into_table("staging_prices");
        insert_stmt.columns([
            "batch_dbid",
            "domain",
            "exchange",
            "symbol",
            "currency",
            "price",
            "date_as_of"
        ]);

        let mut record_count = 0;
        for price in prices {
            record_count += 1;
            
            let Price { symbol, price: price_value, date_as_of } = price;
            
            let (domain, exchange, sym, currency) = match symbol {
                Some(Symbol { id: _, domain, exchange, symbol: sym, currency }) => (
                    domain,
                    exchange,
                    sym,
                    currency
                ),
                None => (
                    String::new(),
                    String::new(),
                    String::new(),
                    String::new()
                ),
            };
            
            let date_as_of = date_as_of.as_ref()
                .map(|ts| DateTime::from_timestamp(ts.seconds, ts.nanos as u32).unwrap_or_default())
                .unwrap_or_default();

            insert_stmt.values_panic([
                batch_dbid.into(),
                domain.into(),
                exchange.into(),
                sym.into(),
                currency.into(),
                price_value.into(),
                date_as_of.into(),
            ]);
        }
        
        if record_count == 0 {
            local_txn.commit_if_owned().await?;
            return Ok(0);
        }

        let query = insert_stmt.to_string(Self::query_builder());
        local_txn.txn().execute_unprepared(&query).await?;
        local_txn.commit_if_owned().await?;
        
        Ok(record_count)
    }

    /// Updates the total record count for a batch using SeaQuery.
    /// 
    /// # Arguments
    /// * `batch_dbid` - The batch database ID to update
    /// * `total_records` - The new total record count
    /// * `txn` - Optional database transaction. If None, a new transaction will be created and committed.
    ///
    /// # Returns
    /// * `Ok(())` if the batch was updated successfully
    /// * `Err` if a database error occurs
    pub async fn update_batch_total_records(
        &self,
        batch_dbid: i64,
        total_records: i32,
        txn: Option<&DatabaseTransaction>,
    ) -> Result<()> {
        let mut local_txn = LocalTxn::new(self.connection(), txn).await?;

        let mut update_stmt = UpdateStatement::new();
        update_stmt.table("staging_batches");
        update_stmt.value("total_records", total_records);
        update_stmt.and_where(sea_query::Expr::col("batch_dbid").eq(batch_dbid));

        let query = update_stmt.to_string(Self::query_builder());
        local_txn.txn().execute_unprepared(&query).await?;
        local_txn.commit_if_owned().await?;
        
        Ok(())
    }

    /// Updates the status of a batch using SeaQuery.
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
    pub async fn update_batch_status(
        &self,
        batch_dbid: i64,
        status: &str,
        error_message: Option<&str>,
        txn: Option<&DatabaseTransaction>,
    ) -> Result<()> {
        let mut local_txn = LocalTxn::new(self.connection(), txn).await?;

        let mut update_stmt = UpdateStatement::new();
        update_stmt.table("staging_batches");
        update_stmt.value("status", status);
        
        if let Some(msg) = error_message {
            update_stmt.value("error_message", msg);
        }
        
        update_stmt.and_where(sea_query::Expr::col("batch_dbid").eq(batch_dbid));

        let query = update_stmt.to_string(Self::query_builder());
        local_txn.txn().execute_unprepared(&query).await?;
        local_txn.commit_if_owned().await?;
        
        Ok(())
    }
} 