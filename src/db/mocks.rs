use anyhow::Result;
use chrono::{DateTime, Utc};
use mockall::*;
use sea_orm::DatabaseTransaction;

use crate::db::api::DataStore;
use crate::db::ingest::api::IngestStore;
use crate::db::models;
use crate::db::users::UserStore;
use crate::db::DatabaseManager;
use crate::portfolio_db::Tx;

// Create a mock for the DataStore trait using mockall
mock! {
    pub DataStoreMock {}

    #[async_trait::async_trait]
    impl DataStore for DataStoreMock {
        async fn with_tx(&self) -> Result<DatabaseManager<DatabaseTransaction>>;
    }

    #[async_trait::async_trait]
    impl IngestStore for DataStoreMock {
        async fn create_batch(
            &self,
            user_dbid: i64,
            batch_type: &str,
            period_start: DateTime<Utc>,
            period_end: DateTime<Utc>,
        ) -> Result<i64>;

        async fn stage_txs(
            &self,
            batch_dbid: i64,
            transactions: Box<dyn Iterator<Item = Tx> + Send>,
        ) -> Result<usize>;

        async fn stage_prices(
            &self,
            batch_dbid: i64,
            prices: Box<dyn Iterator<Item = crate::portfolio_db::Price> + Send>,
        ) -> Result<usize>;

        async fn update_batch_total_records(
            &self,
            batch_dbid: i64,
            total_records: i32,
        ) -> Result<()>;

        async fn update_batch_status<'a>(
            &'a self,
            batch_dbid: i64,
            status: &'a str,
            error_message: Option<&'a str>,
        ) -> Result<()>;

        async fn validate_txs(
            &self,
            batch_dbid: i64,
        ) -> Result<()>;

        async fn create_symbols_and_instruments(
            &self,
            new_symbols: Vec<(String, String, String, String, Option<String>)>,
            disambiguated: bool,
        ) -> Result<Vec<models::Symbol>>;
    }

    #[async_trait::async_trait]
    impl UserStore for DataStoreMock {
        async fn get_user_id_by_email(&self, email: &str) -> Result<Option<i64>>;
    }
}
