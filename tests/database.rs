use chrono::{DateTime, Utc};
use sea_orm::{EntityTrait};
use anyhow::Result;

use portfoliodb::models::{
    Instrument, Identifier, Derivative, Transaction, Price,
    Instruments, Identifiers, Derivatives, Transactions, Prices
};
use portfoliodb::database::DatabaseManager;

mod infra;
use infra::{TestDatabase, populate_database, verify_database, clear_database};

/// Test cases for delete_instruments function
#[tokio::test]
async fn test_delete_instruments() {
    let test_cases = vec![
        (
            TestDatabase::new()
                .add_instrument(
                    Instrument {
                        dbid: 1001,
                        r#type: "STK".to_string(),
                        currency: Some("USD".to_string()),
                    },
                    vec![
                        Identifier {
                            dbid: 2001,
                            instrument_dbid: 0,
                            id: "".to_string(),
                            domain: "GOOGLEFINANCE".to_string(),
                            symbol: "AAPL".to_string(),
                            exchange: "NASDAQ".to_string(),
                            description: "".to_string(),
                        }
                    ],
                    None,
                ),
            TestDatabase::new(),
            "Delete single instrument with identifiers"
        ),
        (
            TestDatabase::new()
                .add_instrument(
                    Instrument {
                        dbid: 1004,
                        r#type: "STK".to_string(),
                        currency: Some("USD".to_string()),
                    },
                    vec![],
                    None,
                )
                .add_instrument(
                    Instrument {
                        dbid: 1005,
                        r#type: "OPT".to_string(),
                        currency: Some("USD".to_string()),
                    },
                    vec![],
                    Some(Derivative {
                        dbid: 5001,
                        instrument_dbid: 1005,
                        underlying_dbid: 1004,
                        expiration_date: DateTime::parse_from_rfc3339("2024-12-31T00:00:00Z").unwrap().with_timezone(&Utc),
                        put_call: "CALL".to_string(),
                        strike_price: 160.0,
                        multiplier: 1.0,
                    }),
                ),
            TestDatabase::new()
                .add_instrument(
                    Instrument {
                        dbid: 1006,
                        r#type: "STK".to_string(),
                        currency: Some("USD".to_string()),
                    },
                    vec![],
                    None,
                ),
            "Delete instrument with derivative (option deleted, underlying remains)"
        ),
    ];
    
    // Connect to test database
    let database_url = std::env::var("DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/portfoliodb".to_string());
    
    let db_manager = DatabaseManager::new(&database_url).await
        .expect("Failed to connect to database");
    
    // Run each test case
    for (before_state, after_state, description) in test_cases {
        println!("Running test: {}", description);
        
        // Clear database before test
        clear_database(&db_manager).await
            .expect("Failed to clear database");
        
        // Populate with before state
        populate_database(&db_manager, &before_state).await
            .expect("Failed to populate database with before state");
        
        // Get all instruments from the database to find their actual IDs
        let conn = db_manager.connection();
        let actual_instruments = Instruments::find().all(conn).await
            .expect("Failed to get instruments from database");
        
        // Get instrument IDs to delete (all instruments in before state)
        // For test case 4, we need to delete only the second instrument (the option)
        let instr_dbids: Vec<i64> = if description.contains("derivative") {
            // For derivative test, only delete the second instrument (the option)
            if actual_instruments.len() >= 2 {
                vec![actual_instruments[1].dbid]
            } else {
                actual_instruments.iter().map(|instrument| instrument.dbid).collect()
            }
        } else {
            // For other tests, delete all instruments
            actual_instruments.iter().map(|instrument| instrument.dbid).collect()
        };
        
        // Call delete_instruments function
        db_manager.delete_instruments(instr_dbids, None).await
            .expect("Failed to delete instruments");
        
        // Verify after state
        let after_verified = verify_database(&db_manager, &after_state).await
            .expect("Failed to verify after state");
        assert!(after_verified, "After state verification failed for: {}", description);
        
        // Clear database for next test
        clear_database(&db_manager).await
            .expect("Failed to clear database after test");
        
        println!("Test passed: {}", description);
    }
}
