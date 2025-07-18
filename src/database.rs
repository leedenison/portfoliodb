use anyhow::Result;
use sea_orm::{
    Database, DatabaseConnection, DatabaseTransaction,
};
use std::sync::Arc;
use tracing::info;

use crate::models::{
    Identifier,
};
use crate::portfolio_db::{DateRange, Tx};

#[derive(Clone)]
pub struct DatabaseManager {
    conn: Arc<DatabaseConnection>,
}

impl DatabaseManager {
    pub async fn new(database_url: &str) -> Result<Self> {
        let conn = Database::connect(database_url).await?;
        Ok(Self {
            conn: Arc::new(conn),
        })
    }

    /// Get a reference to the database connection
    pub fn connection(&self) -> &DatabaseConnection {
        &self.conn
    }

    /// Updates transactions for a specific account within a given time period.
    /// This operation replaces all existing transactions in the specified period with the provided
    /// transactions. If the transactions list is empty, this effectively deletes all transactions
    /// in the period.
    pub async fn update_txs(&self, _period: &DateRange, _txs: &[Tx]) -> Result<()> {
        info!("update_transactions not implemented");

        Ok(())
    }
}
