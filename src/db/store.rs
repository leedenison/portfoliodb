use anyhow::Result;
use sea_orm::{Database, DatabaseConnection, DatabaseTransaction, ConnectionTrait, TransactionTrait};
use crate::db::users::UserStore;
use crate::db::ingest::api::IngestStore;

#[async_trait::async_trait]
pub trait TransactionalStore {
    async fn begin(&self) -> Result<impl TransactionalStore + UserStore + IngestStore + Send + Sync>;
    async fn commit(self) -> Result<()>;
    async fn rollback(self) -> Result<()>;
}

pub struct DataStore<E> {
    exec: E,
}

impl DataStore<DatabaseConnection> {
    pub async fn new(url: &str) -> Result<Self> {
        let conn = Database::connect(url).await?;
        Ok(Self { exec: conn })
    }
}

#[async_trait::async_trait]
impl TransactionalStore for DataStore<DatabaseConnection> {
    async fn begin(&self) -> Result<impl TransactionalStore + UserStore + IngestStore + Send + Sync> {
        let tx = self.exec.begin().await?;
        Ok(DataStore { exec: tx })
    }

    async fn commit(self) -> Result<()> {
        Err(anyhow::anyhow!("Attempted to commit a transaction context while not in a transaction context"))
    }

    async fn rollback(self) -> Result<()> {
        Err(anyhow::anyhow!("Attempted to rollback a transaction context while not in a transaction context"))
    }
}

#[async_trait::async_trait]
impl TransactionalStore for DataStore<DatabaseTransaction> {    
    async fn begin(&self) -> Result<DataStore<DatabaseTransaction>> {
        Err(anyhow::anyhow!("Attempted to enter a transaction context from within an existing transaction context"))
    }

    async fn commit(self) -> Result<()> {
        self.exec.commit().await?;
        Ok(())
    }

    async fn rollback(self) -> Result<()> {
        self.exec.rollback().await?;
        Ok(())
    }
}

impl<E> DataStore<E> 
where
    E: ConnectionTrait + TransactionTrait,
{
    pub fn exec(&self) -> &E { &self.exec }
}