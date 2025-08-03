use anyhow::Result;
use sea_orm::{DatabaseConnection, DatabaseTransaction};

use crate::db::ingest::api::IngestStore;
use crate::db::users::UserStore;

/// Trait defining the complete data store operations for PortfolioDB.
#[async_trait::async_trait]
pub trait DataStore: IngestStore + UserStore {
    fn connection(&self) -> &DatabaseConnection;
    async fn begin(&self) -> Result<DatabaseTransaction>;
} 