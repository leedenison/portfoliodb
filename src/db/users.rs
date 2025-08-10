use anyhow::Result;
use sea_orm::{EntityTrait, QueryFilter, ColumnTrait};

/// Trait defining the user operations for PortfolioDB.
/// This trait abstracts the user operations and allows for easier testing
/// by enabling mock implementations.
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

use crate::db::models::{users, Users};
use super::database::DatabaseManager;

#[async_trait::async_trait]
impl UserStore for DatabaseManager {
    async fn get_user_id_by_email(&self, email: &str) -> Result<Option<i64>> {
        let db = self.connection();
        let user = Users::find()
            .filter(users::Column::Email.eq(email))
            .one(db.as_ref())
            .await?;

        Ok(user.map(|user| user.dbid))
    }
} 
