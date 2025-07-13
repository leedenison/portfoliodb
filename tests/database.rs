mod infra;
use infra::{TestDatabase, populate_database, verify_database, clear_database};

use chrono::{DateTime, Utc};

use portfoliodb::models::{
    Instrument, Identifier, Derivative
};
use portfoliodb::database::DatabaseManager;

#[tokio::test]
async fn test_delete_instruments() {
    let database_url = match std::env::var("DATABASE_URL") {
        Ok(url) => url,
        Err(_) => {
            println!("DATABASE_URL not set, skipping test_delete_instruments");
            return;
        }
    };

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
                            instrument_dbid: 1001,
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
            vec![1001],
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
                            instrument_dbid: 1004,
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
                            instrument_dbid: 1004,
                            id: "".to_string(),
                            domain: "GOOGLEFINANCE".to_string(),
                            symbol: "AAPL".to_string(),
                            exchange: "NASDAQ".to_string(),
                            description: "".to_string(),
                        }
                    ],
                    None,
                ),
            vec![1005],
            "Delete instrument with derivative (option deleted, underlying remains)"
        ),
    ];
    
    let db_manager = DatabaseManager::new(&database_url).await
        .expect("Failed to connect to database");
    
    for (before_state, after_state, instr_dbids_to_delete, description) in test_cases {
        println!("Running test: {}", description);
        
        clear_database(&db_manager).await
            .expect("Failed to clear database");
        
        populate_database(&db_manager, &before_state).await
            .expect("Failed to populate database with before state");
        
        db_manager.delete_instruments(instr_dbids_to_delete, None).await
            .expect("Failed to delete instruments");
        
        verify_database(&db_manager, &after_state).await
            .expect(&format!("After state verification failed for: {}", description));
        
        clear_database(&db_manager).await
            .expect("Failed to clear database after test");
        
        println!("Test passed: {}", description);
    }
}
