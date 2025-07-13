use anyhow::Result;
use std::collections::HashMap;

use portfoliodb::models::{
    Instrument, Identifier, Derivative, Transaction, Price,
    Instruments, Identifiers, Derivatives, Transactions, Prices
};
use portfoliodb::database::DatabaseManager;
use sea_orm::EntityTrait;

/// TestDatabase struct for declaratively specifying test database contents
#[derive(Debug, Clone)]
pub struct TestDatabase {
    pub instruments: Vec<(Instrument, Vec<Identifier>, Option<Derivative>)>,
    pub transactions: Vec<Transaction>,
    pub prices: Vec<Price>,
}

impl TestDatabase {
    pub fn new() -> Self {
        Self {
            instruments: Vec::new(),
            transactions: Vec::new(),
            prices: Vec::new(),
        }
    }

    pub fn add_instrument(
        mut self,
        instrument: Instrument,
        identifiers: Vec<Identifier>,
        derivative: Option<Derivative>,
    ) -> Self {
        self.instruments.push((instrument, identifiers, derivative));
        self
    }

    pub fn add_transaction(mut self, transaction: Transaction) -> Self {
        self.transactions.push(transaction);
        self
    }

    pub fn add_price(mut self, price: Price) -> Self {
        self.prices.push(price);
        self
    }
}

/// Populate the database with the contents of a TestDatabase
/// Ensures data is added in the correct order to satisfy foreign key constraints
/// Requires explicit dbids for all entities
pub async fn populate_database(
    db_manager: &DatabaseManager,
    test_db: &TestDatabase,
) -> Result<()> {
    // Step 1: Create all instruments first (no dependencies)
    let mut instrument_dbids = HashMap::new();
    let mut instrument_index = 0;
    
    for (instrument, identifiers, derivative) in &test_db.instruments {
        // Require explicit dbid
        if instrument.dbid == 0 {
            return Err(anyhow::anyhow!("Explicit dbid required for instrument with dbid 0"));
        }
        
        let instrument_dbid = db_manager.create_instrument(instrument.clone()).await?;
        instrument_dbids.insert(instrument_index, instrument_dbid);
        instrument_index += 1;
        
        // Step 2: Create identifiers for this instrument
        for identifier in identifiers {
            let mut identifier_with_dbid = identifier.clone();
            identifier_with_dbid.instrument_dbid = instrument_dbid;
            
            // Require explicit dbid
            if identifier.dbid == 0 {
                return Err(anyhow::anyhow!("Explicit dbid required for identifier: {}", identifier.id));
            }
            
            db_manager.create_identifier(identifier_with_dbid).await?;
        }
        
        // Step 3: Create derivative if present
        if let Some(derivative) = derivative {
            let mut derivative_with_dbid = derivative.clone();
            derivative_with_dbid.instrument_dbid = instrument_dbid;
            
            // Handle underlying_dbid reference - assume it references the first instrument
            // This is a simplified approach for testing
            if let Some(&underlying_dbid) = instrument_dbids.get(&0) {
                derivative_with_dbid.underlying_dbid = underlying_dbid;
            }
            
            // Require explicit dbid
            if derivative.dbid == 0 {
                return Err(anyhow::anyhow!("Explicit dbid required for derivative"));
            }
            
            db_manager.create_derivative(derivative_with_dbid).await?;
        }
    }
    
    // Step 4: Create transactions (depend on instruments)
    for transaction in &test_db.transactions {
        let mut transaction_with_dbid = transaction.clone();
        // Assume transaction references the first instrument
        if let Some(&instrument_dbid) = instrument_dbids.get(&0) {
            transaction_with_dbid.instrument_dbid = instrument_dbid;
        }
        
        // Require explicit dbid
        if transaction.dbid == 0 {
            return Err(anyhow::anyhow!("Explicit dbid required for transaction"));
        }
        
        db_manager.create_transaction(transaction_with_dbid).await?;
    }
    
    // Step 5: Create prices (depend on instruments)
    for price in &test_db.prices {
        let mut price_with_dbid = price.clone();
        // Assume price references the first instrument
        if let Some(&instrument_dbid) = instrument_dbids.get(&0) {
            price_with_dbid.instrument_dbid = instrument_dbid;
        }
        
        // Require explicit dbid
        if price.dbid == 0 {
            return Err(anyhow::anyhow!("Explicit dbid required for price"));
        }
        
        db_manager.create_price(price_with_dbid).await?;
    }
    
    Ok(())
}

/// Verify that the database matches the expected TestDatabase state
pub async fn verify_database(
    db_manager: &DatabaseManager,
    expected: &TestDatabase,
) -> Result<()> {
    // Get database connection
    let conn = db_manager.connection();
    
    // Verify instruments
    let actual_instruments = Instruments::find().all(conn).await?;
    assert_eq!(
        actual_instruments.len(),
        expected.instruments.len(),
        "Instrument count mismatch: expected {}, got {}",
        expected.instruments.len(),
        actual_instruments.len()
    );
    
    // Verify that expected instrument dbids exist in database
    let expected_instrument_dbids: Vec<i64> = expected.instruments.iter()
        .map(|(instrument, _, _)| instrument.dbid)
        .collect();
    let actual_instrument_dbids: Vec<i64> = actual_instruments.iter()
        .map(|instrument| instrument.dbid)
        .collect();
    
    verify_dbids_exist(&expected_instrument_dbids, &actual_instrument_dbids, "instrument");
    
    // Verify identifiers
    let actual_identifiers = Identifiers::find().all(conn).await?;
    let expected_identifier_count: usize = expected.instruments.iter()
        .map(|(_, identifiers, _)| identifiers.len())
        .sum();
    assert_eq!(
        actual_identifiers.len(),
        expected_identifier_count,
        "Identifier count mismatch: expected {}, got {}",
        expected_identifier_count,
        actual_identifiers.len()
    );
    
    // Verify that expected identifier dbids exist in database
    let expected_identifier_dbids: Vec<i64> = expected.instruments.iter()
        .flat_map(|(_, identifiers, _)| identifiers.iter().map(|identifier| identifier.dbid))
        .collect();
    let actual_identifier_dbids: Vec<i64> = actual_identifiers.iter()
        .map(|identifier| identifier.dbid)
        .collect();
    
    verify_dbids_exist(&expected_identifier_dbids, &actual_identifier_dbids, "identifier");
    
    // Verify derivatives
    let actual_derivatives = Derivatives::find().all(conn).await?;
    let expected_derivative_count = expected.instruments.iter()
        .filter(|(_, _, derivative)| derivative.is_some())
        .count();
    assert_eq!(
        actual_derivatives.len(),
        expected_derivative_count,
        "Derivative count mismatch: expected {}, got {}",
        expected_derivative_count,
        actual_derivatives.len()
    );
    
    // Verify that expected derivative dbids exist in database
    let expected_derivative_dbids: Vec<i64> = expected.instruments.iter()
        .filter_map(|(_, _, derivative)| derivative.as_ref().map(|d| d.dbid))
        .collect();
    let actual_derivative_dbids: Vec<i64> = actual_derivatives.iter()
        .map(|derivative| derivative.dbid)
        .collect();
    
    verify_dbids_exist(&expected_derivative_dbids, &actual_derivative_dbids, "derivative");
    
    // Verify transactions
    let actual_transactions = Transactions::find().all(conn).await?;
    assert_eq!(
        actual_transactions.len(),
        expected.transactions.len(),
        "Transaction count mismatch: expected {}, got {}",
        expected.transactions.len(),
        actual_transactions.len()
    );
    
    // Verify that expected transaction dbids exist in database
    let expected_transaction_dbids: Vec<i64> = expected.transactions.iter()
        .map(|transaction| transaction.dbid)
        .collect();
    let actual_transaction_dbids: Vec<i64> = actual_transactions.iter()
        .map(|transaction| transaction.dbid)
        .collect();
    
    verify_dbids_exist(&expected_transaction_dbids, &actual_transaction_dbids, "transaction");
    
    // Verify prices
    let actual_prices = Prices::find().all(conn).await?;
    assert_eq!(
        actual_prices.len(),
        expected.prices.len(),
        "Price count mismatch: expected {}, got {}",
        expected.prices.len(),
        actual_prices.len()
    );
    
    // Verify that expected price dbids exist in database
    let expected_price_dbids: Vec<i64> = expected.prices.iter()
        .map(|price| price.dbid)
        .collect();
    let actual_price_dbids: Vec<i64> = actual_prices.iter()
        .map(|price| price.dbid)
        .collect();
    
    verify_dbids_exist(&expected_price_dbids, &actual_price_dbids, "price");
    
    Ok(())
}

/// Clear all data from the database
pub async fn clear_database(db_manager: &DatabaseManager) -> Result<()> {
    let conn = db_manager.connection();
    
    // Delete in reverse dependency order
    Prices::delete_many().exec(conn).await?;
    Transactions::delete_many().exec(conn).await?;
    Derivatives::delete_many().exec(conn).await?;
    Identifiers::delete_many().exec(conn).await?;
    Instruments::delete_many().exec(conn).await?;
    
    Ok(())
} 

/// Helper function to verify that expected dbids exist in actual dbids
fn verify_dbids_exist(expected_dbids: &[i64], actual_dbids: &[i64], entity_type: &str) {
    for expected_dbid in expected_dbids {
        assert!(
            actual_dbids.contains(expected_dbid),
            "Expected {} dbid {} not found in database",
            entity_type,
            expected_dbid
        );
    }
}