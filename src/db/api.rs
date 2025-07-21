use anyhow::Result;
use sea_orm::{EntityTrait, QueryFilter, ColumnTrait};
use crate::models::{users, Users};
use super::database::DatabaseManager;

impl DatabaseManager {
    /// Retrieves a user's database ID by their email address.
    /// 
    /// # Arguments
    /// * `email` - The email address to search for.
    ///
    /// # Returns
    /// * `Ok(Some(user_id))` if a user with the given email is found
    /// * `Ok(None)` if no user with the given email exists
    /// * `Err` if a database error occurs
    pub async fn get_user_id_by_email(&self, email: &str) -> Result<Option<i64>> {
        let user = Users::find()
            .filter(users::Column::Email.eq(email))
            .one(self.connection())
            .await?;

        Ok(user.map(|user| user.dbid))
    }
} 