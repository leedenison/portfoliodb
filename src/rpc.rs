use tonic::{Request, Response, Status};
use tracing::info;
use anyhow::Result;
use std::sync::Arc;
use tokio::sync::Mutex;

use crate::portfolio_db_server::PortfolioDb;
use crate::{
    Error, ErrorCode, GetHoldingsRequest, GetHoldingsResponse, GetPricesRequest, GetPricesResponse,
    UpdateInstrumentRequest, UpdateInstrumentResponse, UpdatePricesRequest, UpdatePricesResponse,
    UpdateTxsRequest, UpdateTxsResponse,
};
use crate::database::DatabaseManager;

#[derive(Default)]
pub struct PortfolioDBService {
    db: Arc<Mutex<Option<DatabaseManager>>>,
    database_url: String,
}

impl PortfolioDBService {
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
                    .map_err(|e| Status::internal(format!("Database connection failed: {}", e)))?
            );
        }
        
        Ok(db_guard.as_ref().unwrap().clone())
    }
}

#[tonic::async_trait]
impl PortfolioDb for PortfolioDBService {
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
        let request_data = request.into_inner();
        
        info!("UpdateTxs called with {} transactions", request_data.txs.len());
        
        // Get database manager
        let db = self.db().await?;
        
        // Get the period from the request
        let period = request_data.period
            .ok_or_else(|| Status::invalid_argument("Period is required"))?;
        
        // Update transactions in database
        db.update_txs(&period, &request_data.txs)
            .await
            .map_err(|e| Status::internal(format!("Failed to update transactions: {}", e)))?;
        
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
    /// from the start_date.
    /// 
    /// # Arguments
    /// * `request` - Contains date range and list of account IDs to query
    /// 
    /// # Returns
    /// * List of holdings with account_id, instrument_id, quantity, and date information
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
        _request: Request<UpdatePricesRequest>,
    ) -> Result<Response<UpdatePricesResponse>, Status> {
        // Stub implementation
        info!("UpdatePrices called");
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
    /// Each price entry corresponds to a consecutive day starting from the start_date.
    /// 
    /// # Arguments
    /// * `request` - Contains date range and list of instrument IDs to query
    /// 
    /// # Returns
    /// * List of prices with instrument_id, price, currency, and date information
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

    /// Updates or creates instrument metadata.
    /// 
    /// This operation can be used to add new instruments or update existing instrument
    /// information including symbols, type, currency, and derivative details.
    /// 
    /// # Arguments
    /// * `request` - Contains the instrument data to update or create
    /// 
    /// # Returns
    /// * Success response with OK error code, or error response with details
    async fn update_instrument(
        &self,
        _request: Request<UpdateInstrumentRequest>,
    ) -> Result<Response<UpdateInstrumentResponse>, Status> {
        // Stub implementation
        info!("UpdateInstrument called");
        Ok(Response::new(UpdateInstrumentResponse {
            error: Some(Error {
                code: ErrorCode::Ok as i32,
                message: "Stub implementation".to_string(),
            }),
        }))
    }
} 