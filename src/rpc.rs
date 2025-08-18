use tonic::{Request, Response, Status};
use std::collections::HashMap;
use std::sync::Arc;
use anyhow::{Result, anyhow, bail};

use crate::portfolio_db::{
    portfolio_db_server::PortfolioDb, UpdateTxsRequest, UpdateTxsResponse, Tx, Instrument,
};
use crate::db::api::DataStore;
use crate::db::ingest::api::IngestStore;
use crate::db::DatabaseManager;
use crate::db::models;
use crate::errors::Errors;
use sea_orm::{DatabaseTransaction};
use chrono::{DateTime, Utc};
use pbjson_types::Timestamp;

pub struct Service {
    db_mgr: Arc<dyn DataStore + Send + Sync>,
}

pub struct DisambiguatedIds {
    identifiers: HashMap<(String, String, String), models::Identifier>,
}

impl Service {
    pub fn new(db: Arc<dyn DataStore + Send + Sync>) -> Self {
        Self { db_mgr: db }
    }

    fn db(&self) -> Arc<dyn DataStore + Send + Sync> {
        self.db_mgr.clone()
    }

    /// Validates a DateRange period, ensuring timestamps are not default values and period_end is after period_start
    /// 
    /// # Arguments
    /// * `period` - The DateRange to validate
    /// 
    /// # Returns
    /// * `Ok((DateTime<Utc>, DateTime<Utc>))` - Tuple of (period_start, period_end) if validation passes
    /// * `Err(Status)` - Error with details if validation fails
    fn validate_period(period: &crate::DateRange) -> Result<(DateTime<Utc>, DateTime<Utc>)> {
        let start_ts = period.start.as_ref()
            .ok_or_else(|| anyhow!("Start timestamp is required"))?;
        let start_dt = DateTime::from_timestamp(start_ts.seconds, start_ts.nanos as u32)
            .ok_or_else(|| anyhow!("Invalid start timestamp"))?;
        
        let end_ts = period.end.as_ref()
            .ok_or_else(|| anyhow!("End timestamp is required"))?;
        let end_dt = DateTime::from_timestamp(end_ts.seconds, end_ts.nanos as u32)
            .ok_or_else(|| anyhow!("Invalid end timestamp"))?;

        // Validate that timestamps are not default values (Unix epoch)
        let default_timestamp = DateTime::from_timestamp(0, 0).unwrap();
        if start_dt == default_timestamp {
            bail!("Period start cannot be the default timestamp (Unix epoch)");
        }
        if end_dt == default_timestamp {
            bail!("Period end cannot be the default timestamp (Unix epoch)");
        }

        // Validate that period_end is after period_start
        if end_dt <= start_dt {
            bail!("Period end must be after period start");
        }

        Ok((start_dt, end_dt))
    }

    /// Extracts the authenticated user ID from a request's extensions.
    /// 
    /// # Arguments
    /// * `request` - The tonic request containing user authentication data
    /// 
    /// # Returns
    /// * `Ok(i64)` - The user ID if found and authenticated
    /// * `Err(Status)` - Unauthenticated error if user ID is not found
    fn get_authenticated_user<T>(&self, request: &Request<T>) -> Result<i64> {
        request.extensions().get::<HashMap<String, i64>>()
            .ok_or_else(|| anyhow!("User is not authenticated"))?
            .get("user_id")
            .copied()
            .ok_or_else(|| anyhow!("User is not authenticated"))
    }

    /// Stages transactions and instruments into a batch for processing.
    /// 
    /// # Arguments
    /// * `user_id` - The user ID for the batch
    /// * `broker_key` - The broker key for the batch
    /// * `period_start` - Start of the period
    /// * `period_end` - End of the period
    /// * `txs` - Vector of transactions to stage
    /// * `instruments` - Vector of instruments to stage
    /// 
    /// # Returns
    /// * `Ok(i64)` - The batch ID if successful
    /// * `Err(Status)` - Error with details if staging fails
    async fn stage_txs_batch(
        &self,
        user_id: i64,
        broker_key: String,
        period_start: DateTime<Utc>,
        period_end: DateTime<Utc>,
        txs: Vec<Tx>,
        instruments: Vec<Instrument>,
    ) -> Result<i64> {
        let batch_dbid = self.db().create_batch(
            user_id,
            "TXS_TIMESERIES",
            broker_key.as_str(),
            period_start,
            period_end
        ).await?;

        let tx = self.db().with_tx().await?;
        let total_records = match {
            let mut total_records = tx.stage_instruments(batch_dbid, Box::new(instruments.into_iter())).await?;
            total_records += tx.stage_txs(batch_dbid, Box::new(txs.into_iter())).await?;
            Ok::<usize, anyhow::Error>(total_records)
        } {
            Ok(total_records) => total_records,
            Err(e) => {
                let _ = self.db().update_batch_status(batch_dbid, "FAILED", Some(&e.to_string()));
                return Err(e)
            }
        };
        tx.commit().await?;
        
        self.db().update_batch_total_records(batch_dbid, total_records as i32).await?;
        self.db().update_batch_status(batch_dbid, "PROCESSING", None).await?;
        
        Ok(batch_dbid)
    }

    /// Resolves broker symbol descriptions and corresponding symbol hints to the symbol dbid 
    /// in the database.  If the symbol dbid is not found, the symbol description and symbol hint
    /// are used to look up the symbol using any configured disambiguation services, and the symbol
    /// is created with a new instrument if it does not exist.
    /// 
    /// # Arguments
    /// * `batch_dbid` - The batch ID to process
    /// * `user_id` - Optional user ID for filtering user owned mappings
    /// 
    /// # Returns
    /// * `Ok(())` - Success if disambiguation is successful
    /// * `Err(anyhow::Error)` - Error if disambiguation fails
    #[cfg(feature = "disambiguate")]
    async fn disambiguate_ids(
        &self,  
        batch_dbid: i64,
        user_id: i64,
    ) -> Result<DisambiguatedIds> {
        // Stub implementation when disambiguation feature is enabled
        tracing::info!("Disambiguation feature enabled - skipping identifier disambiguation for batch {} (user: {})", batch_dbid, user_id);
        Ok(DisambiguatedIds {
            identifiers: HashMap::new(),
        })
    }

    /// Resolves identifiers to the identifier dbid in the database.  
    /// If no identifier dbid is found, a new identifier with a new instrument is created.
    /// 
    /// # Arguments
    /// * `batch_dbid` - The batch ID to process
    /// * `user_id` - Optional user ID for filtering user owned mappings
    /// 
    /// # Returns
    /// * `Ok(())` - Success if disambiguation is successful
    /// * `Err(anyhow::Error)` - Error if disambiguation fails
    #[cfg(not(feature = "disambiguate"))]
    async fn disambiguate_ids(
        &self,
        tx: &DatabaseManager<DatabaseTransaction>,
        batch_dbid: i64,
        user_id: i64,
    ) -> Result<DisambiguatedIds> {
        let mut result = DisambiguatedIds {
            identifiers: HashMap::new(),
        };

        

        // Stub implementation when disambiguation feature is disabled
        tracing::info!("Disambiguation feature disabled - skipping identifier disambiguation for batch {} (user: {})", batch_dbid, user_id);
        Ok(result)
    }
}

#[tonic::async_trait]
impl PortfolioDb for Service {
    
    /// Updates transactions for a specific account within a given time period.
    ///
    /// This operation replaces all existing transactions in the specified period with the provided
    /// transactions. If the transactions list is empty, this effectively deletes all transactions
    /// in the period.
    ///
    /// # Arguments
    /// * `request` - Contains date range, and list of transactions to update
    ///
    /// # Returns
    /// * Success response with OK error code, or error response with details
    async fn update_txs(
        &self,
        request: Request<UpdateTxsRequest>,
    ) -> std::result::Result<Response<UpdateTxsResponse>, Status> {
        let user_id = self.get_authenticated_user(&request)
            .map_err(Errors::unauthenticated)?;

        let req = request.into_inner();

        let UpdateTxsRequest { period, txs, broker, instruments } = req;

        let period = period
            .ok_or_else(|| Errors::invalid_argument(anyhow!("Period is required")))?;
        let (period_start, period_end) = Self::validate_period(&period)
            .map_err(Errors::invalid_argument)?;

        let broker_key = broker
            .ok_or_else(|| Errors::invalid_argument(anyhow!("Broker is required")))?
            .key;

        let batch_dbid = self.stage_txs_batch(
            user_id,
            broker_key,
            period_start,
            period_end,
            txs,
            instruments).await
            .map_err(|e| Errors::internal(e.context("Failed to stage transactions")))?;
        
        let tx = self.db().with_tx().await.map_err(Errors::internal)?;

        let disambiguated_ids = self.disambiguate_ids(&tx, batch_dbid, user_id).await
            .map_err(|e| Errors::internal(e.context("Failed to disambiguate identifiers")))?;

        tx.commit().await.map_err(Errors::internal)?;

        Ok(Response::new(UpdateTxsResponse {}))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::mocks::MockDataStoreMock;
    use mockall::predicate::{eq, always};
    use serde_json::json;

    fn test_tx() -> Tx {
        serde_json::from_value(json!({
            "account_id": "account1",
            "identifier": {
                "namespace": "NASDAQ",
                "domain": "NASDAQ",
                "identifier": "AAPL"
            },
            "units": 10.0,
            "unit_price": 150.0,
            "currency": "USD",
            "trade_date": "2022-01-01T00:00:00Z",
            "settled_date": "2022-01-02T00:00:00Z",
            "tx_type": "BUY"
        })).expect("Failed to deserialize transaction from JSON")
    }

    fn test_stock() -> Instrument {
        serde_json::from_value(json!({
            "identifiers": [
                {
                    "namespace": "NASDAQ",
                    "domain": "NASDAQ",
                    "identifier": "AAPL"
                }
            ],
            "currency": "USD",
            "status": "ACTIVE",
            "listing_mic": "NASDAQ",
            "type": "STK"
        })).expect("Failed to deserialize stock from JSON")
    }

    fn test_option() -> Instrument {
        serde_json::from_value(json!({
            "identifiers": [
                {
                    "namespace": "OCC",
                    "identifier": "AAPL250919C00150000"
                }
            ],
            "currency": "USD",
            "status": "ACTIVE",
            "listing_mic": "NASDAQ",
            "type": "OPT",
            "derivative": {
                "underlying": {
                    "namespace": "NASDAQ",
                    "domain": "NASDAQ",
                    "identifier": "AAPL"
                },
                "option": {
                    "option_style": "AMERICAN",
                    "strike_price": 150.0,
                    "expiration_date": "2022-01-01T00:00:00Z"
                }
            }
        })).expect("Failed to deserialize option from JSON")
    }

    #[tokio::test]
    async fn test_stage_txs_success() {
        let user_id = 1;
        let period_start = DateTime::from_timestamp(1640995200, 0).unwrap();
        let period_end = DateTime::from_timestamp(1641081600, 0).unwrap();
        let txs = vec![test_tx()];
        let instruments = vec![test_stock(), test_option()];
        let expected_batch_id = 1;
        let expected_instrument_records = 4;
        let expected_tx_records = 1;
        let expected_total_records = expected_instrument_records + expected_tx_records;

        let mut mock = MockDataStoreMock::new();

        mock.expect_create_batch()
            .times(1)
            .with(
                eq(user_id),
                eq("TXS_TIMESERIES"),
                eq("test_broker"),
                eq(period_start),
                eq(period_end)
            )
            .returning(move |_, _, _, _, _| Ok(expected_batch_id));

        mock.expect_stage_instruments()
            .times(1)
            .with(eq(expected_batch_id), always())
            .returning(move |_, instruments| {
                let instruments_vec: Vec<Instrument> = instruments.collect();
                assert_eq!(instruments_vec.len(), 2, "Expected 2 instruments to be staged");
                Ok(expected_instrument_records)
            });

        mock.expect_stage_txs()
            .times(1)
            .with(eq(expected_batch_id), always())
            .returning(move |_, txs| {
                let txs_vec: Vec<Tx> = txs.collect();
                assert_eq!(txs_vec.len(), 1, "Expected 1 transaction to be staged");
                Ok(expected_tx_records)
            });

        mock.expect_update_batch_total_records()
            .times(1)
            .with(eq(expected_batch_id), eq(expected_total_records as i32))
            .returning(|_, _| Ok(()));

        mock.expect_update_batch_status()
            .times(1)
            .with(eq(expected_batch_id), eq("PROCESSING"), always())
            .returning(|_, _, _| Ok(()));

        let service = Service::new(Arc::new(mock));

        let result = service.stage_txs_batch(
            user_id,
            "test_broker".to_string(),
            period_start,
            period_end,
            txs,
            instruments).await;

        assert!(result.is_ok(), "Expected stage_txs to succeed");
        let batch_id = result.unwrap();
        assert_eq!(batch_id, expected_batch_id, "Expected batch ID {} but got {}", expected_batch_id, batch_id);
        println!("✓ Test passed: stage_txs success case");
    }
}

