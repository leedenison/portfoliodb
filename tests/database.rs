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
            vec![1001], // Delete the single instrument
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
                )
                .add_instrument(
                    Instrument {
                        dbid: 1005,
                        r#type: "OPT".to_string(),
                        currency: Some("USD".to_string()),
                    },
                    vec![
                        Identifier {
                            dbid: 2002,
                            instrument_dbid: 0,
                            id: "".to_string(),
                            domain: "OCC".to_string(),
                            symbol: "AAPL240816C00195000".to_string(),
                            exchange: "AMEX".to_string(),
                            description: "".to_string(),
                        }
                    ],
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
                        dbid: 1004,
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
            vec![1005], // Delete only the option instrument
            "Delete instrument with derivative (option deleted, underlying remains)"
        ),
    ];
    
    // Connect to test database
    let database_url = std::env::var("DATABASE_URL")
        .unwrap_or_else(|_| "postgres://localhost/portfoliodb".to_string());
    
    let db_manager = DatabaseManager::new(&database_url).await
        .expect("Failed to connect to database");
    
    // Run each test case
    for (before_state, after_state, instr_dbids_to_delete, description) in test_cases {
        println!("Running test: {}", description);
        
        // Clear database before test
        clear_database(&db_manager).await
            .expect("Failed to clear database");
        
        // Populate with before state
        populate_database(&db_manager, &before_state).await
            .expect("Failed to populate database with before state");
        
        // Call delete_instruments function with the specified IDs
        db_manager.delete_instruments(instr_dbids_to_delete, None).await
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
