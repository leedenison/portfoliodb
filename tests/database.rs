use sea_orm::{Database, DatabaseConnection, EntityTrait, QueryFilter, TransactionTrait, DatabaseTransaction, ColumnTrait, PaginatorTrait};
use anyhow::Result;
use std::sync::Arc;

use portfoliodb::database::DatabaseManager;
use portfoliodb::models::{Instruments, Identifiers, Transactions, Prices, Derivatives};
use portfoliodb::models::identifiers::Model as Identifier;
use portfoliodb::models::instruments::Column as InstrumentCol;
use portfoliodb::models::identifiers::Column as IdCol;

#[tokio::test]
async fn test_delete_instruments() -> Result<()> {
    // Get database URL from environment or use default
    let database_url = std::env::var("DATABASE_URL")
        .unwrap_or_else(|_| "postgres://portfoliodb:portfoliodb_test_password@localhost:5432/portfoliodb_test".to_string());

    let test_db = TestDatabase::new(&database_url).await?;
    
    // Clean up any existing data
    test_db.cleanup().await?;

    // Create test data within a transaction
    let txn = test_db.conn.begin().await?;
    
    // Create test instruments
    let instrument_dbids = test_db.create_test_instruments(&txn).await?;
    assert!(!instrument_dbids.is_empty(), "Should have created test instruments");

    // Create associated identifiers
    test_db.create_test_identifiers(&instrument_dbids, &txn).await?;
    
    // Create associated transactions
    test_db.create_test_transactions(&instrument_dbids, &txn).await?;
    
    // Create associated prices
    test_db.create_test_prices(&instrument_dbids, &txn).await?;
    
    // Create associated derivatives
    test_db.create_test_derivatives(&instrument_dbids, &txn).await?;

    // Commit the transaction to make the data visible
    txn.commit().await?;

    // Verify data was created
    assert!(test_db.verify_instruments_exist(&instrument_dbids).await?, "Instruments should exist");
    assert!(test_db.verify_identifiers_exist(&instrument_dbids).await?, "Identifiers should exist");

    // Test the delete_instruments functionality
    // We'll test the private method through the public merge_instruments method
    // by creating a scenario where we merge instruments (which internally calls delete_instruments)
    
    // First, let's get the identifiers from the database that we created
    let existing_identifiers = Identifiers::find()
        .filter(IdCol::InstrumentDbid.is_in(instrument_dbids.clone()))
        .all(&*test_db.conn)
        .await?;
    
    assert!(!existing_identifiers.is_empty(), "Should have existing identifiers");

    // Merge instruments (this will internally call delete_instruments for some instruments)
    let merged_instrument_id = test_db.db_manager.merge_instruments(&existing_identifiers).await?;
    assert!(merged_instrument_id.is_some(), "Should have merged instruments");

    // Verify that some instruments were deleted (the ones that were merged)
    // The first instrument should remain, others should be deleted
    let remaining_instruments = vec![instrument_dbids[0]];
    let deleted_instruments = vec![instrument_dbids[1], instrument_dbids[2]];

    assert!(test_db.verify_instruments_exist(&remaining_instruments).await?, "Remaining instrument should exist");
    assert!(test_db.verify_instruments_deleted(&deleted_instruments).await?, "Deleted instruments should not exist");

    // Verify that identifiers for deleted instruments are also deleted
    assert!(test_db.verify_identifiers_deleted(&deleted_instruments).await?, "Identifiers for deleted instruments should be deleted");

    // Verify that identifiers for remaining instrument still exist
    assert!(test_db.verify_identifiers_exist(&remaining_instruments).await?, "Identifiers for remaining instrument should exist");

    // Clean up test data
    test_db.cleanup().await?;

    Ok(())
}

#[tokio::test]
async fn test_delete_instruments_empty_list() -> Result<()> {
    // Get database URL from environment or use default
    let database_url = std::env::var("DATABASE_URL")
        .unwrap_or_else(|_| "postgres://portfoliodb:portfoliodb_test_password@localhost:5432/portfoliodb_test".to_string());

    let test_db = TestDatabase::new(&database_url).await?;
    
    // Clean up any existing data
    test_db.cleanup().await?;

    // Test that delete_instruments handles empty list gracefully
    // This is tested through the merge_instruments method with empty identifiers
    let empty_identifiers: Vec<Identifier> = vec![];
    
    let result = test_db.db_manager.merge_instruments(&empty_identifiers).await?;
    assert!(result.is_none(), "Should return None for empty identifiers");

    // Clean up test data
    test_db.cleanup().await?;

    Ok(())
} 

/// Test database setup and cleanup utilities
pub struct TestDatabase {
    pub db_manager: DatabaseManager,
    pub conn: Arc<DatabaseConnection>,
}

impl TestDatabase {
    /// Creates a new test database connection
    pub async fn new(database_url: &str) -> Result<Self> {
        let conn = Database::connect(database_url).await?;
        let db_manager = DatabaseManager::new(database_url).await?;
        
        Ok(Self {
            db_manager,
            conn: Arc::new(conn),
        })
    }

    /// Creates test instruments with associated identifiers
    pub async fn create_test_instruments(&self, txn: &DatabaseTransaction) -> Result<Vec<i64>> {
        let instrument_data = vec![
            ("AAPL", "STOCK", Some("USD")),
            ("MSFT", "STOCK", Some("USD")),
            ("GOOGL", "STOCK", Some("USD")),
        ];

        let mut instrument_dbids = Vec::new();

        for (name, instrument_type, currency) in instrument_data {
            let instrument = Instruments::insert(
                portfoliodb::models::instruments::ActiveModel {
                    name: sea_orm::ActiveValue::Set(name.to_string()),
                    r#type: sea_orm::ActiveValue::Set(instrument_type.to_string()),
                    currency: sea_orm::ActiveValue::Set(currency.map(|c| c.to_string())),
                    ..Default::default()
                }
            )
            .exec(txn)
            .await?;

            instrument_dbids.push(instrument.last_insert_id);
        }

        Ok(instrument_dbids)
    }

    /// Creates test identifiers for the given instrument IDs
    pub async fn create_test_identifiers(
        &self,
        instrument_dbids: &[i64],
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        let identifier_data = vec![
            ("YAHOO", "AAPL", "NASDAQ", "Apple Inc."),
            ("YAHOO", "MSFT", "NASDAQ", "Microsoft Corporation"),
            ("YAHOO", "GOOGL", "NASDAQ", "Alphabet Inc."),
        ];

        for (i, (domain, symbol, exchange, description)) in identifier_data.iter().enumerate() {
            if i < instrument_dbids.len() {
                Identifiers::insert(
                    portfoliodb::models::identifiers::ActiveModel {
                        instrument_dbid: sea_orm::ActiveValue::Set(instrument_dbids[i]),
                        domain: sea_orm::ActiveValue::Set(domain.to_string()),
                        symbol: sea_orm::ActiveValue::Set(symbol.to_string()),
                        exchange: sea_orm::ActiveValue::Set(exchange.to_string()),
                        description: sea_orm::ActiveValue::Set(description.to_string()),
                        ..Default::default()
                    }
                )
                .exec(txn)
                .await?;
            }
        }

        Ok(())
    }

    /// Creates test transactions for the given instrument IDs
    pub async fn create_test_transactions(
        &self,
        instrument_dbids: &[i64],
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        for (_i, instrument_dbid) in instrument_dbids.iter().enumerate() {
            Transactions::insert(
                portfoliodb::models::transactions::ActiveModel {
                    instrument_dbid: sea_orm::ActiveValue::Set(*instrument_dbid),
                    account_id: sea_orm::ActiveValue::Set("test_account".to_string()),
                    units: sea_orm::ActiveValue::Set(100.0),
                    unit_price: sea_orm::ActiveValue::Set(Some(150.0)),
                    currency: sea_orm::ActiveValue::Set("USD".to_string()),
                    trade_date: sea_orm::ActiveValue::Set(chrono::Utc::now()),
                    settled_date: sea_orm::ActiveValue::Set(None),
                    tx_type: sea_orm::ActiveValue::Set("BUY".to_string()),
                    ..Default::default()
                }
            )
            .exec(txn)
            .await?;
        }

        Ok(())
    }

    /// Creates test prices for the given instrument IDs
    pub async fn create_test_prices(
        &self,
        instrument_dbids: &[i64],
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        for (_i, instrument_dbid) in instrument_dbids.iter().enumerate() {
            Prices::insert(
                portfoliodb::models::prices::ActiveModel {
                    instrument_dbid: sea_orm::ActiveValue::Set(*instrument_dbid),
                    price: sea_orm::ActiveValue::Set(152.0),
                    price_date: sea_orm::ActiveValue::Set(chrono::Utc::now()),
                    ..Default::default()
                }
            )
            .exec(txn)
            .await?;
        }

        Ok(())
    }

    /// Creates test derivatives for the given instrument IDs
    pub async fn create_test_derivatives(
        &self,
        instrument_dbids: &[i64],
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        for (_i, instrument_dbid) in instrument_dbids.iter().enumerate() {
            Derivatives::insert(
                portfoliodb::models::derivatives::ActiveModel {
                    instrument_dbid: sea_orm::ActiveValue::Set(*instrument_dbid),
                    underlying_dbid: sea_orm::ActiveValue::Set(*instrument_dbid),
                    put_call: sea_orm::ActiveValue::Set("CALL".to_string()),
                    strike_price: sea_orm::ActiveValue::Set(150.0),
                    multiplier: sea_orm::ActiveValue::Set(1.0),
                    expiration_date: sea_orm::ActiveValue::Set(chrono::Utc::now()),
                    ..Default::default()
                }
            )
            .exec(txn)
            .await?;
        }

        Ok(())
    }

    /// Cleans up all test data
    pub async fn cleanup(&self) -> Result<()> {
        let txn = self.conn.begin().await?;

        // Delete in reverse order of dependencies
        Derivatives::delete_many().exec(&txn).await?;
        Prices::delete_many().exec(&txn).await?;
        Transactions::delete_many().exec(&txn).await?;
        Identifiers::delete_many().exec(&txn).await?;
        Instruments::delete_many().exec(&txn).await?;

        txn.commit().await?;
        Ok(())
    }

    /// Verifies that instruments exist
    pub async fn verify_instruments_exist(&self, instrument_dbids: &[i64]) -> Result<bool> {
        let count = Instruments::find()
            .filter(InstrumentCol::Dbid.is_in(instrument_dbids.to_vec()))
            .count(&*self.conn)
            .await?;
        
        Ok(count == instrument_dbids.len() as u64)
    }

    /// Verifies that instruments don't exist
    pub async fn verify_instruments_deleted(&self, instrument_dbids: &[i64]) -> Result<bool> {
        let count = Instruments::find()
            .filter(InstrumentCol::Dbid.is_in(instrument_dbids.to_vec()))
            .count(&*self.conn)
            .await?;
        
        Ok(count == 0)
    }

    /// Verifies that identifiers exist for the given instrument IDs
    pub async fn verify_identifiers_exist(&self, instrument_dbids: &[i64]) -> Result<bool> {
        let count = Identifiers::find()
            .filter(IdCol::InstrumentDbid.is_in(instrument_dbids.to_vec()))
            .count(&*self.conn)
            .await?;
        
        Ok(count > 0)
    }

    /// Verifies that identifiers don't exist for the given instrument IDs
    pub async fn verify_identifiers_deleted(&self, instrument_dbids: &[i64]) -> Result<bool> {
        let count = Identifiers::find()
            .filter(IdCol::InstrumentDbid.is_in(instrument_dbids.to_vec()))
            .count(&*self.conn)
            .await?;
        
        Ok(count == 0)
    }
}