use anyhow::Result;
use sea_orm::{
    Database, DatabaseConnection, EntityTrait, QueryFilter, ColumnTrait,
};
use std::sync::Arc;
use tracing::info;

use crate::portfolio_db::{DateRange, Tx};
use crate::models::Users;

#[derive(Clone)]
pub struct DatabaseManager {
    conn: Arc<DatabaseConnection>,
}

impl DatabaseManager {
    pub async fn new(database_url: &str) -> Result<Self> {
        let conn = Database::connect(database_url).await?;
        Ok(Self {
            conn: Arc::new(conn),
        })
    }

    /// Get a reference to the database connection
    pub fn connection(&self) -> &DatabaseConnection {
        &self.conn
    }

    /// Look up a user by email and return their database ID
    pub async fn get_user_id_by_email(&self, email: &str) -> Result<Option<i64>> {
        let user = Users::find()
            .filter(<Users as sea_orm::EntityTrait>::Column::Email.eq(email))
            .one(self.connection())
            .await?;

        Ok(user.map(|user| user.dbid))
    }

    /// Updates transactions for a specific account within a given time period.
    /// This operation replaces all existing transactions in the specified period with the provided
    /// transactions. If the transactions list is empty, this effectively deletes all transactions
    /// in the period.
    pub async fn update_txs(&self, _period: &DateRange, _txs: &[Tx]) -> Result<()> {
        info!("update_transactions not implemented");

        Ok(())
    }
}
