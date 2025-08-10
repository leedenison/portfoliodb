use anyhow::Result;
use sea_orm::{
    Database, DatabaseConnection, TransactionTrait,
};
use std::sync::Arc;
use super::api::DataStore;
use super::executor::DatabaseExecutor;

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

    /// Returns a clone of the underlying database connection.
    /// This is used for simple operations that don't need transaction control.
    pub fn connection(&self) -> Arc<DatabaseConnection> {
        Arc::clone(&self.conn)
    }
}

#[async_trait::async_trait]
impl DataStore for DatabaseManager {
    fn executor(&self) -> DatabaseExecutor {
        DatabaseExecutor::from_db(Arc::clone(&self.conn))
    }

    async fn begin(&self) -> Result<DatabaseExecutor> {
        let tx = self.conn.begin().await.map_err(|e| anyhow::anyhow!("Failed to begin transaction: {}", e))?;
        Ok(DatabaseExecutor::from_tx(tx))
    }
}
