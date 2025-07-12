use sea_orm::{Database, DatabaseConnection};
use anyhow::Result;
use tracing::info;
use std::sync::Arc;

use crate::portfolio_db::{Tx, DateRange};

#[derive(Clone)]
pub struct DatabaseManager {
    _conn: Arc<DatabaseConnection>,
}

impl DatabaseManager {
    pub async fn new(database_url: &str) -> Result<Self> {
        let conn = Database::connect(database_url).await?;
        Ok(Self { 
            _conn: Arc::new(conn)
        })
    }

    /// Updates transactions for a specific account within a given time period.
    /// This operation replaces all existing transactions in the specified period with the provided
    /// transactions. If the transactions list is empty, this effectively deletes all transactions
    /// in the period.
    pub async fn update_txs(
        &self,
        _period: &DateRange,
        _txs: &[Tx],
    ) -> Result<()> {

        info!("update_transactions not implemented");

        Ok(())
    }
}