use anyhow::Result;
use sea_orm::{
    Database, DatabaseConnection, DatabaseTransaction, TransactionTrait,
};
use std::sync::Arc;
use super::api::DataStore;

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
}

#[async_trait::async_trait]
impl DataStore for DatabaseManager {
    fn connection(&self) -> &DatabaseConnection {
        &self.conn
    }

    async fn begin(&self) -> Result<DatabaseTransaction> {
        self.conn.begin().await.map_err(|e| anyhow::anyhow!("Failed to begin transaction: {}", e))
    }
}
