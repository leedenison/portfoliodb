use anyhow::Result;
use sea_orm::{
    Database, DatabaseConnection, DatabaseTransaction, TransactionTrait,
};
use sea_query::{PostgresQueryBuilder, QueryBuilder};
use std::sync::Arc;

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

    pub fn connection(&self) -> &DatabaseConnection {
        &self.conn
    }

    /// Begins a new database transaction
    pub async fn begin(&self) -> Result<DatabaseTransaction> {
        self.conn.begin().await.map_err(|e| anyhow::anyhow!("Failed to begin transaction: {}", e))
    }

    pub fn query_builder() -> impl QueryBuilder {
        PostgresQueryBuilder
    }
}
