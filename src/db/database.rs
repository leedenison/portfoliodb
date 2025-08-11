use anyhow::Result;
use sea_orm::{Database, DatabaseConnection, DatabaseTransaction, ConnectionTrait, TransactionTrait};
use crate::db::api::DataStore;

#[derive(Clone)]
pub struct DatabaseManager<E> {
    exec: E,
}

impl DatabaseManager<DatabaseConnection> {
    pub async fn new(url: &str) -> Result<Self> {
        let conn = Database::connect(url).await?;
        Ok(Self { exec: conn })
    }

    pub async fn begin(&self) -> Result<DatabaseTransaction> {
        let tx = self.exec.begin().await?;
        Ok(tx)
    }
}

#[async_trait::async_trait]
impl DataStore for DatabaseManager<DatabaseConnection> {
    async fn with_tx(&self) -> Result<DatabaseManager<DatabaseTransaction>> {
        let tx = self.exec.begin().await?;
        Ok(DatabaseManager { exec: tx })
    }
}

impl DatabaseManager<DatabaseTransaction> {    
    pub async fn begin(&self) -> Result<DatabaseTransaction> {
        let tx = self.exec.begin().await?;
        Ok(tx)
    }

    pub async fn commit(self) -> Result<()> {
        self.exec.commit().await?;
        Ok(())
    }

    pub async fn rollback(self) -> Result<()> {
        self.exec.rollback().await?;
        Ok(())
    }
}

#[async_trait::async_trait]
impl DataStore for DatabaseManager<DatabaseTransaction> {
    async fn with_tx(&self) -> Result<DatabaseManager<DatabaseTransaction>> {
        Err(anyhow::anyhow!("Attempted to enter a transaction context from within an existing transaction context"))
    }
}

impl<E> DatabaseManager<E> 
where
    E: ConnectionTrait + TransactionTrait,
{
    pub fn executor(&self) -> &E { &self.exec }
}