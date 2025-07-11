use sea_orm::{Database, DatabaseConnection, EntityTrait, QueryFilter, ColumnTrait, Set, Condition};
use anyhow::Result;
use chrono::{DateTime, Utc};
use prost_types::Timestamp;
use tracing::info;
use std::sync::Arc;
use std::collections::HashMap;

use crate::models::{Transactions, TransactionActiveModel, Instruments, InstrumentActiveModel, Symbols, SymbolActiveModel};
use crate::models::transactions::Column;
use crate::portfolio_db::{Tx, DateRange, Instrument};

#[derive(Clone)]
pub struct DatabaseManager {
    conn: Arc<DatabaseConnection>,
}

impl DatabaseManager {
    pub async fn new(database_url: &str) -> Result<Self> {
        let conn = Database::connect(database_url).await?;
        Ok(Self { 
            conn: Arc::new(conn)
        })
    }

    /// Updates transactions for a specific account within a given time period.
    /// This operation replaces all existing transactions in the specified period with the provided
    /// transactions. If the transactions list is empty, this effectively deletes all transactions
    /// in the period.
    pub async fn update_txs(
        &self,
        period: &DateRange,
        txs: &[Tx],
    ) -> Result<()> {

        info!("update_transactions not implemented");

        Ok(())
    }
}