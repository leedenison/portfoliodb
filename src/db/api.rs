use crate::db::database::DatabaseManager;
use crate::db::ingest::api::IngestStore;
use crate::db::users::UserStore;
use anyhow::Result;
use sea_orm::DatabaseTransaction;

/// Trait defining the complete data store operations for PortfolioDB.
#[async_trait::async_trait]
pub trait DataStore: IngestStore + UserStore {
    async fn with_tx(&self) -> Result<DatabaseManager<DatabaseTransaction>>;
}
