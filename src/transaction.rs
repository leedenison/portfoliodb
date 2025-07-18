use sea_orm::{DatabaseConnection, DatabaseTransaction, DbErr, TransactionTrait};
/// Wrapper that ensures a local transaction is created if none was provided.
pub enum LocalTxn<'a> {
    Borrowed(&'a DatabaseTransaction),
    Owned(Option<DatabaseTransaction>),
}

impl<'a> LocalTxn<'a> {
    pub async fn new(
        conn: &'a DatabaseConnection,
        maybe_txn: Option<&'a DatabaseTransaction>,
    ) -> Result<Self, DbErr> {
        match maybe_txn {
            Some(txn) => Ok(Self::Borrowed(txn)),
            None => {
                let txn = conn.begin().await?;
                Ok(Self::Owned(Some(txn)))
            }
        }
    }

    pub fn txn(&self) -> &DatabaseTransaction {
        match self {
            LocalTxn::Borrowed(txn) => txn,
            LocalTxn::Owned(Some(txn)) => txn,
            LocalTxn::Owned(None) => panic!("Transaction already committed or rolled back"),
        }
    }

    pub async fn commit_if_owned(&mut self) -> Result<(), DbErr> {
        if let LocalTxn::Owned(txn_opt) = self {
            if let Some(txn) = txn_opt.take() {
                txn.commit().await
            } else {
                Ok(())
            }
        } else {
            Ok(())
        }
    }

    pub async fn rollback_if_owned(&mut self) -> Result<(), DbErr> {
        if let LocalTxn::Owned(txn_opt) = self {
            if let Some(txn) = txn_opt.take() {
                txn.rollback().await
            } else {
                Ok(())
            }
        } else {
            Ok(())
        }
    }

    pub fn into_inner(self) -> Option<DatabaseTransaction> {
        match self {
            LocalTxn::Owned(Some(txn)) => Some(txn),
            _ => None,
        }
    }
}
