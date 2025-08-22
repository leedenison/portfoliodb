use crate::db::store::TransactionalStore;
use crate::db::ingest::api::IngestStore;
use crate::db::users::UserStore;
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

pub struct SimpleResolver<D, R>
where
    D: TransactionalStore + IngestStore + UserStore + Send + Sync,
    R: IdResolver + Send + Sync,
{
    db_mgr: Arc<D>,
    id_resolver: R,
}

impl<D, R> SimpleResolver<D, R>
where
    D: TransactionalStore + IngestStore + UserStore + Send + Sync,
    R: IdResolver + Send + Sync,
{
    pub fn new(db_mgr: Arc<D>, id_resolver: R) -> Self {
        Self {
            db_mgr,
            id_resolver,
        }
    }

    fn db(&self) -> Arc<D> {
        self.db_mgr.clone()
    }
}

#[async_trait::async_trait]
impl<D, R> StagingResolver for SimpleResolver<D, R>
where
    D: TransactionalStore + IngestStore + UserStore + Send + Sync,
    R: IdResolver + Send + Sync,
{
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
