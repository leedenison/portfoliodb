use crate::db::api::DataStore;
use crate::portfolio_db::{Identifier, Instrument};
use anyhow::Result;
use std::sync::Arc;

#[async_trait::async_trait]
pub trait IdResolver {
    fn name(&self) -> String;
    async fn resolve(&self, ids: Vec<Identifier>) -> Result<Vec<Instrument>>;
}

#[async_trait::async_trait]
pub trait StagingResolver {
    /// Resolves identifiers in the supplied batch.
    ///
    /// The StagingIdentifiers and StagingInstruments tables will be updated
    /// to contain resolved identifiers and instruments for each of the sources
    /// which were used to resolve the identifiers.
    ///
    /// # Arguments
    /// * `batch_dbid` - The batch ID to process
    ///
    /// # Returns
    /// * `Ok(())` - Success if resolution is successful
    /// * `Err(anyhow::Error)` - Error if resolution fails
    async fn resolve(&self, batch_dbid: i64) -> Result<()>;
}

pub struct SimpleResolver {
    db_mgr: Arc<dyn DataStore + Send + Sync>,
    id_resolver: Box<dyn IdResolver + Send + Sync>,
}

impl SimpleResolver {
    pub fn new(
        db_mgr: Arc<dyn DataStore + Send + Sync>,
        id_resolver: Box<dyn IdResolver + Send + Sync>,
    ) -> Self {
        Self {
            db_mgr,
            id_resolver,
        }
    }

    fn db(&self) -> Arc<dyn DataStore + Send + Sync> {
        self.db_mgr.clone()
    }
}

#[async_trait::async_trait]
impl StagingResolver for SimpleResolver {
    /// Resolves identifiers in the supplied batch.
    ///
    /// Uses a single source to resolve identifiers and instruments.
    ///
    /// # Arguments
    /// * `batch_dbid` - The batch ID to process
    ///
    /// # Returns
    /// * `Ok(())` - Success if resolution is successful
    /// * `Err(anyhow::Error)` - Error if resolution fails
    async fn resolve(&self, batch_dbid: i64) -> Result<()> {
        let db = self.db();
        let new_ids = db.unresolved_identifiers(batch_dbid).await?;

        let ids = new_ids.into_iter().map(|id| id.into()).collect();
        let instruments = self.id_resolver.resolve(ids).await?;
        let instruments: Vec<_> = instruments
            .into_iter()
            .map(|instrument| instrument.into())
            .collect();
        db.stage_instruments(
            batch_dbid,
            self.id_resolver.name(),
            Box::new(instruments.into_iter()),
        )
        .await?;

        Ok(())
    }
}

pub struct PriorityResolver {}

// TODO: This should be updated to query multiple IdResolvers
// TODO  according to a priority queue in which IdResolvers of the same
// TODO  priority are run in parallel.  The priority is configured in the
// TODO  database.
// TODO  Duplicate identifiers may be resolved multiple times and stored
// TODO  in StagingIdentifiers, once for each source.
#[async_trait::async_trait]
impl StagingResolver for PriorityResolver {
    async fn resolve(&self, _batch_dbid: i64) -> Result<()> {
        Ok(())
    }
}
