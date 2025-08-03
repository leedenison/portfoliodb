use anyhow::Result;
use std::collections::HashMap;
use std::sync::Arc;
use tokio::sync::Mutex;
use tonic::{Request, Response, Status};
use tracing::info;
use chrono::{DateTime, Utc};
use pbjson_types::Timestamp;

use crate::db::DatabaseManager;
use crate::portfolio_db_server::PortfolioDb;
use crate::models::{Symbol, SymbolDescription};
use crate::{
    Error, ErrorCode, GetHoldingsRequest, GetHoldingsResponse, GetPricesRequest, GetPricesResponse,
    UpdateBrokerRequest, UpdateBrokerResponse, DeleteBrokerRequest, DeleteBrokerResponse,
    UpdatePricesRequest, UpdatePricesResponse, UpdateTxsRequest, UpdateTxsResponse,
};

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
    let period_start = timestamp_to_datetime(period.start.as_ref()
        .ok_or_else(|| Status::invalid_argument("Start timestamp is required"))?)?;
    let period_end = timestamp_to_datetime(period.end.as_ref()
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

#[derive(Default)]
pub struct Service {
    db: Arc<Mutex<Option<DatabaseManager>>>,
    database_url: String,
}

impl Service {
    pub fn new(database_url: String) -> Self {
        Self {
            db: Arc::new(Mutex::new(None)),
            database_url,
        }
    }

    async fn db(&self) -> Result<DatabaseManager, Status> {
        let mut db_guard = self.db.lock().await;

        if db_guard.is_none() {
            *db_guard = Some(
                DatabaseManager::new(&self.database_url)
                    .await
                    .map_err(|e| Status::internal(format!("Database connection failed: {}", e)))?,
            );
        }

        Ok(db_guard.as_ref().unwrap().clone())
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
        let (period_start, period_end) = validate_period(&period)?;

        let db = self.db().await?;

        let batch_dbid = db.create_batch(
            Some(user_id),
            "TXS_TIMESERIES",
            period_start,
            period_end,
            None
        ).await
        .map_err(|e| Status::internal(format!("Failed to create batch: {}", e)))?;

        let total_records = db.stage_txs(batch_dbid, txs, None).await
        .map_err(|e| {
            let msg = format!("Failed to stage transactions: {}", e);
            db.update_batch_status(batch_dbid, "FAILED", Some(&msg), None);
            Status::internal(msg)
        })?;

        db.update_batch_total_records(batch_dbid, total_records as i32, None).await
        .map_err(|e| Status::internal(format!("Failed to update batch total records: {}", e)))?;

        if let Err(e) = db.update_batch_status(batch_dbid, "PROCESSING", None, None).await {
            tracing::warn!("Failed to update batch status to PROCESSING: {}", e);
        }

        let identifiers = disambiguate_instruments(batch_dbid, None).await
        .map_err(|e| Status::internal(format!("Failed to disambiguate instruments: {}", e)))?;

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

        let UpdatePricesRequest { period, prices } = req;

        let period = period
            .ok_or_else(|| Status::invalid_argument("Period is required"))?;
        let (period_start, period_end) = validate_period(&period)?;

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

/// Resolves broker symbol descriptions and corresponding symbol hints to the instrument dbid 
/// in the database.  If the instrument dbid is not found, the symbol description and symbol hint
/// are used to look up the instrument using any configured disambiguation services, and the instrument
/// is created if it does not exist.
/// 
/// # Arguments
/// * `batch_dbid` - The batch ID to process
/// * `user_id` - Optional user ID for filtering user owned mappings
/// 
/// # Returns
/// * `Ok(HashMap<(SymbolDescription, Symbol), i64>)` - Map of symbol description and symbol hint to instrument dbid
/// * `Err(anyhow::Error)` - Error if disambiguation fails
#[cfg(feature = "disambiguate")]
async fn disambiguate_instruments(
    batch_dbid: i64,
    user_id: Option<i64>,
) -> Result<HashMap<(SymbolDescription, Symbol), i64>, anyhow::Error> {
    // TODO: Implement actual disambiguation logic
    tracing::info!("Disambiguating instruments for batch {} (user: {:?})", batch_dbid, user_id);
    
    // Return empty HashMap as stub implementation
    Ok(HashMap::new())
}

/// Resolves broker symbol descriptions and corresponding symbol hints to the instrument dbid 
/// in the database.  If no intrument dbid is found, a new instrument is created.
/// 
/// # Arguments
/// * `batch_dbid` - The batch ID to process
/// * `user_id` - Optional user ID for filtering user owned mappings
/// 
/// # Returns
/// * `Ok(HashMap<(SymbolDescription, Symbol), i64>)` - Map of symbol description and symbol hint to instrument dbid
/// * `Err(anyhow::Error)` - Error if disambiguation fails
#[cfg(not(feature = "disambiguate"))]
async fn disambiguate_instruments(
    batch_dbid: i64,
    user_id: Option<i64>,
) -> Result<HashMap<(SymbolDescription, Symbol), i64>, anyhow::Error> {
    // Stub implementation when disambiguation feature is disabled
    tracing::info!("Disambiguation feature disabled - skipping instrument disambiguation for batch {} (user: {:?})", batch_dbid, user_id);
    
    // Return empty HashMap as stub implementation
    Ok(HashMap::new())
}
