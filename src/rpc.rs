use tonic::{Request, Response, Status};
use tracing::info;
use std::collections::HashMap;
use std::sync::Arc;

use crate::portfolio_db::{
    portfolio_db_server::PortfolioDb, Error, ErrorCode, GetHoldingsRequest, GetHoldingsResponse,
    GetPricesRequest, GetPricesResponse, UpdateBrokerRequest, UpdateBrokerResponse,
    UpdatePricesRequest, UpdatePricesResponse, UpdateTxsRequest, UpdateTxsResponse,
    DeleteBrokerRequest, DeleteBrokerResponse, SymbolDescription, Tx,
};
use crate::db::api::DataStore;
use crate::db::DatabaseManager;
use crate::db::ingest::models::staging_txs;
use crate::db::ingest::models::StagingTx;
use crate::db::ingest::api::IngestStore;
use crate::db::models;
use sea_orm::DatabaseTransaction;
use chrono::{DateTime, Utc};
use pbjson_types::Timestamp;

pub struct Service {
    db_mgr: Arc<dyn DataStore + Send + Sync>,
}

pub struct DisambiguatedIds {
    symbol_descriptions: HashMap<String, SymbolDescription>,
    symbols:  HashMap<(String, String, String), models::Symbol>,
}

impl Service {
    pub fn new(db: Arc<dyn DataStore + Send + Sync>) -> Self {
        Self { db_mgr: db }
    }

    fn db(&self) -> Arc<dyn DataStore + Send + Sync> {
        self.db_mgr.clone()
    }

    /// Converts a protobuf Timestamp to a chrono DateTime<Utc>
    /// 
    /// # Arguments
    /// * `timestamp` - Protobuf timestamp
    /// 
    /// # Returns
    /// * `Ok(DateTime<Utc>)` if conversion is successful
    /// * `Err(Status)` if timestamp is invalid
    fn timestamp_to_datetime(timestamp: &Timestamp) -> Result<DateTime<Utc>, Status> {
        DateTime::from_timestamp(timestamp.seconds, timestamp.nanos as u32)
            .ok_or_else(|| Status::invalid_argument("Invalid timestamp"))
    }

    /// Validates a DateRange period, ensuring timestamps are not default values and period_end is after period_start
    /// 
    /// # Arguments
    /// * `period` - The DateRange to validate
    /// 
    /// # Returns
    /// * `Ok((DateTime<Utc>, DateTime<Utc>))` - Tuple of (period_start, period_end) if validation passes
    /// * `Err(Status)` - Error with details if validation fails
    fn validate_period(period: &crate::DateRange) -> Result<(DateTime<Utc>, DateTime<Utc>), Status> {
        let period_start = Self::timestamp_to_datetime(period.start.as_ref()
            .ok_or_else(|| Status::invalid_argument("Start timestamp is required"))?)?;
        let period_end = Self::timestamp_to_datetime(period.end.as_ref()
            .ok_or_else(|| Status::invalid_argument("End timestamp is required"))?)?;

        // Validate that timestamps are not default values (Unix epoch)
        let default_timestamp = DateTime::from_timestamp(0, 0).unwrap();
        if period_start == default_timestamp {
            return Err(Status::invalid_argument("Period start cannot be the default timestamp (Unix epoch)"));
        }
        if period_end == default_timestamp {
            return Err(Status::invalid_argument("Period end cannot be the default timestamp (Unix epoch)"));
        }

        // Validate that period_end is after period_start
        if period_end <= period_start {
            return Err(Status::invalid_argument("Period end must be after period start"));
        }

        Ok((period_start, period_end))
    }

    /// Extracts the authenticated user ID from a request's extensions.
    /// 
    /// # Arguments
    /// * `request` - The tonic request containing user authentication data
    /// 
    /// # Returns
    /// * `Ok(i64)` - The user ID if found and authenticated
    /// * `Err(Status)` - Unauthenticated error if user ID is not found
    fn get_authenticated_user<T>(&self, request: &Request<T>) -> Result<i64, Status> {
        request.extensions().get::<HashMap<String, i64>>()
            .ok_or_else(|| Status::unauthenticated("User ID not found"))?
            .get("user_id")
            .copied()
            .ok_or_else(|| Status::unauthenticated("User ID not found"))
    }

    /// Stages transactions into a batch for processing.
    /// 
    /// # Arguments
    /// * `user_id` - The user ID for the batch
    /// * `txs` - Vector of transactions to stage
    /// * `period_start` - Start of the period
    /// * `period_end` - End of the period
    /// 
    /// # Returns
    /// * `Ok(i64)` - The batch ID if successful
    /// * `Err(Status)` - Error with details if staging fails
    async fn stage_txs(
        &self,
        user_id: i64,
        period_start: DateTime<Utc>,
        period_end: DateTime<Utc>,
        txs: Vec<Tx>,
    ) -> Result<i64, Status> {
        let batch_dbid = self.db().create_batch(
            user_id,
            "TXS_TIMESERIES",
            period_start,
            period_end
        ).await
        .map_err(|e| Status::internal(format!("Failed to create batch: {}", e)))?;

        let total_records = self.db().stage_txs(batch_dbid, Box::new(txs.into_iter())).await
        .map_err(|e| {
            let msg = format!("Failed to stage transactions: {}", e);
            let _ = self.db().update_batch_status(batch_dbid, "FAILED", Some(&msg));
            Status::internal(msg)
        })?;

        self.db().update_batch_total_records(batch_dbid, total_records as i32).await
        .map_err(|e| Status::internal(format!("Failed to update batch total records: {}", e)))?;

        if let Err(e) = self.db().update_batch_status(batch_dbid, "PROCESSING", None).await {
            tracing::warn!("Failed to update batch status to PROCESSING: {}", e);
        }
        
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
    ) -> Result<DisambiguatedIds, anyhow::Error> {
        // Stub implementation when disambiguation feature is enabled
        tracing::info!("Disambiguation feature enabled - skipping symbol disambiguation for batch {} (user: {})", batch_dbid, user_id);
        Ok(DisambiguatedIds {
            symbol_descriptions: HashMap::new(),
            symbols: HashMap::new(),
        })
    }

    /// Resolves broker symbol descriptions and corresponding symbol hints to the symbol dbid 
    /// in the database.  If no symbol dbid is found, a new symbol with a new instrument is created.
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
    ) -> Result<DisambiguatedIds, anyhow::Error> {
        let mut result = DisambiguatedIds {
            symbol_descriptions: HashMap::new(),
            symbols: HashMap::new(),
        };

        let staged_symbols_with_existing = staging_txs::Entity::all_complete_symbols_with_existing(
            tx.executor(), 
            batch_dbid).await?;
        let mut new_symbols_to_create = Vec::new();

        // Find existing symbols
        for (staged_tx, existing_symbol) in staged_symbols_with_existing {
            let StagingTx { domain, exchange, symbol, symbol_currency: currency, .. } = staged_tx;

            if let Some(existing_symbol) = existing_symbol {
                result.symbols.insert((domain, exchange, symbol), existing_symbol);
            } else {
                new_symbols_to_create.push((domain, exchange, symbol, currency, None));
            }
        }

        // Create new symbols and instruments
        if !new_symbols_to_create.is_empty() {
            let created_symbols = tx.create_symbols_and_instruments(new_symbols_to_create, false).await?;
            
            for symbol in created_symbols {
                result.symbols.insert((symbol.domain.clone(), symbol.exchange.clone(), symbol.symbol.clone()), symbol);
            }
        }

        // Step 5: select all non-empty symbol descriptions with corresponding symbol hints from
        //         staged transactions and join them to symbol descriptions that exist in the database

        // Step 6: create new symbol descriptions for any symbol descriptions that do not
        //         exist in the database.  If the symbol description is associated with a complete 
        //         symbol (ie. has non-empty domain, exchange, and symbol) in step 5, ensure that the
        //         symbol_dbid for new symbol descriptions is looked up from the mapping in step 4

        // Step 7: add mappings from symbol description dbid to SymbolDescription struct in result

        // Stub implementation when disambiguation feature is disabled
        tracing::info!("Disambiguation feature disabled - skipping symbol disambiguation for batch {} (user: {})", batch_dbid, user_id);
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
    ) -> Result<Response<UpdateTxsResponse>, Status> {
        let user_id = self.get_authenticated_user(&request)?;
        let req = request.into_inner();

        let UpdateTxsRequest { period, txs } = req;

        let period = period
            .ok_or_else(|| Status::invalid_argument("Period is required"))?;
        let (period_start, period_end) = Self::validate_period(&period)?;

        let batch_dbid = self.stage_txs(user_id, period_start, period_end, txs).await?;

        self.db().validate_txs(batch_dbid).await
            .map_err(|e| Status::internal(format!("Invalid transactions in batch {}: {}", batch_dbid, e)))?;
        
        let tx = self.db().with_tx().await
            .map_err(|e| Status::internal(format!("Failed to begin transaction: {}", e)))?;

        let disambiguated_ids = self.disambiguate_ids(&tx, batch_dbid, user_id).await
            .map_err(|e| Status::internal(format!("Failed to disambiguate symbols: {}", e)))?;

        tx.commit().await
            .map_err(|e| Status::internal(format!("Failed to commit transaction: {}", e)))?;

        Ok(Response::new(UpdateTxsResponse {
            error: Some(Error {
                code: ErrorCode::Ok as i32,
                message: String::new(),
            }),
        }))
    }

    /// Retrieves holdings timeseries data for specified accounts within a date range.
    ///
    /// Returns a dense timeseries of holdings for each account-instrument pair
    /// in the requested period. Each holding entry corresponds to a consecutive day starting
    /// from the start.
    ///
    /// # Arguments
    /// * `request` - Contains date range and list of account IDs to query
    ///
    /// # Returns
    /// * List of holdings with account_id, symbol_dbid, symbol_description_dbid, quantity, and date information
    async fn get_holdings(
        &self,
        _request: Request<GetHoldingsRequest>,
    ) -> Result<Response<GetHoldingsResponse>, Status> {
        // Stub implementation
        info!("GetHoldings called");
        Ok(Response::new(GetHoldingsResponse {
            holdings: vec![],
            error: Some(Error {
                code: ErrorCode::Ok as i32,
                message: "Stub implementation".to_string(),
            }),
        }))
    }

    /// Updates price data for instruments within a given time period.
    ///
    /// This operation replaces all existing prices in the specified period with the provided
    /// prices. If the prices list is empty, this effectively deletes all prices in the period.
    ///
    /// # Arguments
    /// * `request` - Contains date range and list of prices to update
    ///
    /// # Returns
    /// * Success response with OK error code, or error response with details
    async fn update_prices(
        &self,
        request: Request<UpdatePricesRequest>,
    ) -> Result<Response<UpdatePricesResponse>, Status> {
        let user_id = self.get_authenticated_user(&request)?;
        let req = request.into_inner();

        let UpdatePricesRequest { period, prices: _ } = req;

        let period = period
            .ok_or_else(|| Status::invalid_argument("Period is required"))?;
        let (period_start, period_end) = Self::validate_period(&period)?;

        // TODO: Implement actual price update logic
        info!("UpdatePrices called with period: {} to {}", period_start, period_end);
        
        Ok(Response::new(UpdatePricesResponse {
            error: Some(Error {
                code: ErrorCode::Ok as i32,
                message: "Stub implementation".to_string(),
            }),
        }))
    }

    /// Retrieves price timeseries data for specified instruments within a date range.
    ///
    /// Returns a dense timeseries of prices for each instrument in the requested period.
    /// Each price entry corresponds to a consecutive day starting from the start.
    ///
    /// # Arguments
    /// * `request` - Contains date range and list of instrument IDs to query
    ///
    /// # Returns
    /// * List of prices with instrument_dbid, price, currency, and date information
    async fn get_prices(
        &self,
        _request: Request<GetPricesRequest>,
    ) -> Result<Response<GetPricesResponse>, Status> {
        // Stub implementation
        info!("GetPrices called");
        Ok(Response::new(GetPricesResponse {
            prices: vec![],
            error: Some(Error {
                code: ErrorCode::Ok as i32,
                message: "Stub implementation".to_string(),
            }),
        }))
    }

    /// Updates or creates broker metadata.
    ///
    /// # Arguments
    /// * `request` - Broker data to update or create
    ///
    /// # Returns
    /// * Success or error response
    async fn update_broker(
        &self,
        _request: Request<UpdateBrokerRequest>,
    ) -> Result<Response<UpdateBrokerResponse>, Status> {
        // Stub implementation
        info!("UpdateBroker called");
        Ok(Response::new(UpdateBrokerResponse {
            error: Some(Error {
                code: ErrorCode::Ok as i32,
                message: "Stub implementation".to_string(),
            }),
        }))
    }

    /// Deletes a broker by ID.
    ///
    /// # Arguments
    /// * `request` - Broker ID to delete
    ///
    /// # Returns
    /// * Success or error response
    async fn delete_broker(
        &self,
        _request: Request<DeleteBrokerRequest>,
    ) -> Result<Response<DeleteBrokerResponse>, Status> {
        // Stub implementation
        info!("DeleteBroker called");
        Ok(Response::new(DeleteBrokerResponse {
            error: Some(Error {
                code: ErrorCode::Ok as i32,
                message: "Stub implementation".to_string(),
            }),
        }))
    }
}

