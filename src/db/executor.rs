use async_trait::async_trait;
use sea_orm::{DatabaseConnection, DatabaseTransaction, DbErr, TransactionTrait};
use std::sync::Arc;

#[async_trait]
pub trait Executor {
    async fn begin(&mut self) -> Result<&DatabaseTransaction, DbErr>;
    async fn save(&mut self) -> Result<&DatabaseTransaction, DbErr>;
    async fn commit(&mut self) -> Result<(), DbErr>;
    async fn rollback(&mut self) -> Result<(), DbErr>;
}

pub enum DatabaseExecutor {
    Conn {
        db: Arc<DatabaseConnection>,
        tx: Option<DatabaseTransaction>,
    },
    Tx {
        tx: DatabaseTransaction,
        savepoint: Option<DatabaseTransaction>,
    },
}

impl DatabaseExecutor {
    pub fn from_db(db: Arc<DatabaseConnection>) -> Self {
        Self::Conn { db, tx: None }
    }

    pub fn from_tx(tx: DatabaseTransaction) -> Self {
        Self::Tx {
            tx,
            savepoint: None,
        }
    }
}

#[async_trait]
impl Executor for DatabaseExecutor {
    async fn begin(&mut self) -> Result<&DatabaseTransaction, DbErr> {
        match self {
            Self::Conn { db, tx } => {
                if tx.is_none() {
                    *tx = Some(db.begin().await?);
                }
                Ok(tx.as_ref().unwrap())
            }
            Self::Tx { tx, .. } => {
                // already in a transaction; no-op
                Ok(tx)
            }
        }
    }

    async fn save(&mut self) -> Result<&DatabaseTransaction, DbErr> {
        match self {
            Self::Conn { db, tx } => {
                if tx.is_none() {
                    *tx = Some(db.begin().await?);
                }
                Ok(tx.as_ref().unwrap())
            }
            Self::Tx { tx, savepoint } => {
                *savepoint = Some(tx.begin().await?);
                Ok(savepoint.as_ref().unwrap())
            }
        }
    }

    async fn commit(&mut self) -> Result<(), DbErr> {
        match self {
            Self::Conn { tx, .. } => {
                if let Some(tx) = tx.take() {
                    tx.commit().await?;
                }
                Ok(())
            }
            Self::Tx { savepoint, .. } => {
                if let Some(sp) = savepoint.take() {
                    sp.commit().await?;
                }
                Ok(())
            }
        }
    }

    async fn rollback(&mut self) -> Result<(), DbErr> {
        match self {
            Self::Conn { tx, .. } => {
                if let Some(tx) = tx.take() {
                    tx.rollback().await?;
                }
                Ok(())
            }
            Self::Tx { savepoint, .. } => {
                if let Some(sp) = savepoint.take() {
                    sp.rollback().await?;
                }
                Ok(())
            }
        }
    }
}
