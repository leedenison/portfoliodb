use anyhow::Result;
use sea_orm::{Database, DatabaseConnection, DatabaseTransaction, ConnectionTrait, TransactionTrait};
use crate::db::users::UserStore;
use crate::db::ingest::api::IngestStore;
use crate::automock;

pub trait TransactionalStore {
    type Store: TransactionalStore + UserStore + IngestStore + Send + Sync;

    fn begin(&self) -> impl Future<Output = Result<Self::Store>> + Send;        
    fn commit(self) -> impl Future<Output = Result<()>> + Send;
    fn rollback(self) -> impl Future<Output = Result<()>> + Send;
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

impl TransactionalStore for DataStore<DatabaseConnection> {
    type Store = DataStore<DatabaseTransaction>;

    fn begin(&self) -> impl Future<Output = Result<Self::Store>> + Send {
        async move {
            let tx = self.exec.begin().await?;
            Ok(DataStore { exec: tx })
        }
    }

    fn commit(self) -> impl Future<Output = Result<()>> + Send {
        async move {
            Err(anyhow::anyhow!("Attempted to commit a transaction context while not in a transaction context"))
        }
    }

    fn rollback(self) -> impl Future<Output = Result<()>> + Send {
        async move {
            Err(anyhow::anyhow!("Attempted to rollback a transaction context while not in a transaction context"))
        }
    }
}

impl TransactionalStore for DataStore<DatabaseTransaction> {    
    type Store = DataStore<DatabaseTransaction>;

    fn begin(&self) -> impl Future<Output = Result<Self::Store>> + Send {
        async move {
            Err(anyhow::anyhow!("Attempted to enter a transaction context from within an existing transaction context"))
        }
    }

    fn commit(self) -> impl Future<Output = Result<()>> + Send {
        async move {
            self.exec.commit().await?;
            Ok(())
        }
    }

    fn rollback(self) -> impl Future<Output = Result<()>> + Send {
        async move {
            self.exec.rollback().await?;
            Ok(())
        }
    }
}

impl<E> DataStore<E> 
where
    E: ConnectionTrait + TransactionTrait,
{
    pub fn exec(&self) -> &E { &self.exec }
}