use anyhow::Result;

use crate::db::ingest::api::IngestStore;
use crate::db::users::UserStore;
use crate::db::executor::DatabaseExecutor;

/// Trait defining the complete data store operations for PortfolioDB.
#[async_trait::async_trait]
pub trait DataStore: IngestStore + UserStore {
    fn executor(&self) -> DatabaseExecutor;
    async fn begin(&self) -> Result<DatabaseExecutor>;
} 