use sea_orm::{Database, DatabaseConnection, EntityTrait, QueryFilter, TransactionTrait, DatabaseTransaction, ColumnTrait, PaginatorTrait};
use anyhow::Result;
use std::sync::Arc;

use portfoliodb::database::DatabaseManager;
use portfoliodb::models::{Instruments, Symbols, Transactions, Prices, Derivatives};
use portfoliodb::models::symbols::Model as Symbol;
use portfoliodb::models::instruments::Column as InstrumentCol;
use portfoliodb::models::symbols::Column as SymCol;

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

    /// Creates test instruments with associated symbols
    pub async fn create_test_instruments(&self, txn: &DatabaseTransaction) -> Result<Vec<i64>> {
        let instrument_data = vec![
            ("AAPL", "STOCK", Some("USD")),
            ("MSFT", "STOCK", Some("USD")),
            ("GOOGL", "STOCK", Some("USD")),
        ];

        let mut instrument_ids = Vec::new();

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

            instrument_ids.push(instrument.last_insert_id);
        }

        Ok(instrument_ids)
    }

    /// Creates test symbols for the given instrument IDs
    pub async fn create_test_symbols(
        &self,
        instrument_ids: &[i64],
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        let symbol_data = vec![
            ("YAHOO", "AAPL", "NASDAQ", "Apple Inc."),
            ("YAHOO", "MSFT", "NASDAQ", "Microsoft Corporation"),
            ("YAHOO", "GOOGL", "NASDAQ", "Alphabet Inc."),
        ];

        for (i, (domain, symbol, exchange, description)) in symbol_data.iter().enumerate() {
            if i < instrument_ids.len() {
                Symbols::insert(
                    portfoliodb::models::symbols::ActiveModel {
                        instrument_id: sea_orm::ActiveValue::Set(instrument_ids[i]),
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
        instrument_ids: &[i64],
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        for (i, instrument_id) in instrument_ids.iter().enumerate() {
            Transactions::insert(
                portfoliodb::models::transactions::ActiveModel {
                    instrument_id: sea_orm::ActiveValue::Set(*instrument_id),
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
        instrument_ids: &[i64],
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        for (i, instrument_id) in instrument_ids.iter().enumerate() {
            Prices::insert(
                portfoliodb::models::prices::ActiveModel {
                    instrument_id: sea_orm::ActiveValue::Set(*instrument_id),
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
        instrument_ids: &[i64],
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        for (i, instrument_id) in instrument_ids.iter().enumerate() {
            Derivatives::insert(
                portfoliodb::models::derivatives::ActiveModel {
                    instrument_id: sea_orm::ActiveValue::Set(*instrument_id),
                    underlying_instrument_id: sea_orm::ActiveValue::Set(*instrument_id),
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
        Symbols::delete_many().exec(&txn).await?;
        Instruments::delete_many().exec(&txn).await?;

        txn.commit().await?;
        Ok(())
    }

    /// Verifies that instruments exist
    pub async fn verify_instruments_exist(&self, instrument_ids: &[i64]) -> Result<bool> {
        let count = Instruments::find()
            .filter(InstrumentCol::Id.is_in(instrument_ids.to_vec()))
            .count(&*self.conn)
            .await?;
        
        Ok(count == instrument_ids.len() as u64)
    }

    /// Verifies that instruments don't exist
    pub async fn verify_instruments_deleted(&self, instrument_ids: &[i64]) -> Result<bool> {
        let count = Instruments::find()
            .filter(InstrumentCol::Id.is_in(instrument_ids.to_vec()))
            .count(&*self.conn)
            .await?;
        
        Ok(count == 0)
    }

    /// Verifies that symbols exist for the given instrument IDs
    pub async fn verify_symbols_exist(&self, instrument_ids: &[i64]) -> Result<bool> {
        let count = Symbols::find()
            .filter(SymCol::InstrumentId.is_in(instrument_ids.to_vec()))
            .count(&*self.conn)
            .await?;
        
        Ok(count > 0)
    }

    /// Verifies that symbols don't exist for the given instrument IDs
    pub async fn verify_symbols_deleted(&self, instrument_ids: &[i64]) -> Result<bool> {
        let count = Symbols::find()
            .filter(SymCol::InstrumentId.is_in(instrument_ids.to_vec()))
            .count(&*self.conn)
            .await?;
        
        Ok(count == 0)
    }
}

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
    let instrument_ids = test_db.create_test_instruments(&txn).await?;
    assert!(!instrument_ids.is_empty(), "Should have created test instruments");

    // Create associated symbols
    test_db.create_test_symbols(&instrument_ids, &txn).await?;
    
    // Create associated transactions
    test_db.create_test_transactions(&instrument_ids, &txn).await?;
    
    // Create associated prices
    test_db.create_test_prices(&instrument_ids, &txn).await?;
    
    // Create associated derivatives
    test_db.create_test_derivatives(&instrument_ids, &txn).await?;

    // Verify data was created
    assert!(test_db.verify_instruments_exist(&instrument_ids).await?, "Instruments should exist");
    assert!(test_db.verify_symbols_exist(&instrument_ids).await?, "Symbols should exist");

    // Test the delete_instruments functionality
    // We'll test the private method through the public merge_instruments method
    // by creating a scenario where we merge instruments (which internally calls delete_instruments)
    
    // First, let's get the symbols from the database that we created
    let existing_symbols = Symbols::find()
        .filter(SymCol::InstrumentId.is_in(instrument_ids.clone()))
        .all(&*test_db.conn)
        .await?;
    
    assert!(!existing_symbols.is_empty(), "Should have existing symbols");

    // Merge instruments (this will internally call delete_instruments for some instruments)
    let merged_instrument_id = test_db.db_manager.merge_instruments(&existing_symbols).await?;
    assert!(merged_instrument_id.is_some(), "Should have merged instruments");

    // Verify that some instruments were deleted (the ones that were merged)
    // The first instrument should remain, others should be deleted
    let remaining_instruments = vec![instrument_ids[0]];
    let deleted_instruments = vec![instrument_ids[1], instrument_ids[2]];

    assert!(test_db.verify_instruments_exist(&remaining_instruments).await?, "Remaining instrument should exist");
    assert!(test_db.verify_instruments_deleted(&deleted_instruments).await?, "Deleted instruments should not exist");

    // Verify that symbols for deleted instruments are also deleted
    assert!(test_db.verify_symbols_deleted(&deleted_instruments).await?, "Symbols for deleted instruments should be deleted");

    // Verify that symbols for remaining instrument still exist
    assert!(test_db.verify_symbols_exist(&remaining_instruments).await?, "Symbols for remaining instrument should exist");

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
    // This is tested through the merge_instruments method with empty symbols
    let empty_symbols: Vec<Symbol> = vec![];
    
    let result = test_db.db_manager.merge_instruments(&empty_symbols).await?;
    assert!(result.is_none(), "Should return None for empty symbols");

    // Clean up test data
    test_db.cleanup().await?;

    Ok(())
} 
