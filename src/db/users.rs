use anyhow::Result;
use sea_orm::{EntityTrait, QueryFilter, ColumnTrait, TransactionTrait, ConnectionTrait};

use crate::db::models::{users, Users};
use crate::db::store::DataStore;

#[async_trait::async_trait]
pub trait UserStore {
    /// Retrieves a user's database ID by their email address.
    /// 
    /// # Arguments
    /// * `email` - The email address to search for.
    ///
    /// # Returns
    /// * `Ok(Some(user_id))` if a user with the given email is found
    /// * `Ok(None)` if no user with the given email exists
    /// * `Err` if a database error occurs
    async fn get_user_id_by_email(&self, email: &str) -> Result<Option<i64>>;
}

#[async_trait::async_trait]
impl<E> UserStore for DataStore<E>
where
    E: ConnectionTrait + TransactionTrait + Send + Sync,
{
    async fn get_user_id_by_email(&self, email: &str) -> Result<Option<i64>> {
        let user = Users::find()
            .filter(users::Column::Email.eq(email))
            .one(self.exec())
            .await?;

        Ok(user.map(|user| user.dbid))
    }
} 
