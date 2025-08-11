use portfoliodb::db::DatabaseManager;
use portfoliodb::db::api::DataStore;
use portfoliodb::db::ingest::api::IngestStore;
use portfoliodb::db::ingest::models::staging_txs;
use portfoliodb::portfolio_db::Tx;
use sea_orm::{EntityTrait, QueryFilter, ColumnTrait};
use serde_json::{json, Value};
use std::env;
use portfoliodb::db::executor::{DatabaseExecutor::Conn, DatabaseExecutor::Tx as TxExec};

#[cfg(test)]
mod tests {
    use super::*;

    /// Creates a DatabaseManager from the DATABASE_URL environment variable.
    /// 
    /// # Returns
    /// * `Ok(DatabaseManager)` if the database connection was established successfully
    /// * `Err` if DATABASE_URL is not set or if the connection fails
    async fn db() -> anyhow::Result<DatabaseManager> {
        let database_url = env::var("DATABASE_URL")
            .map_err(|_| anyhow::anyhow!("DATABASE_URL environment variable is not set"))?;
        
        DatabaseManager::new(&database_url).await
    }

    /// Test case structure for stage_txs tests
    #[derive(Debug)]
    struct TestCase {
        name: String,
        input_txs_json: Value,
        expected_staging_txs_json: Value,
    }

    #[cfg(feature = "test_class_database")]
    #[tokio::test]
    async fn test_stage_txs() {
        let db = db().await.expect("Failed to connect to database");

        // Define test cases
        let test_cases = vec![
            TestCase {
                name: "Basic buy transaction".to_string(),
                input_txs_json: json!([
                    {
                        "id": "tx1",
                        "account_id": "account1",
                        "description": {
                            "id": "desc1",
                            "broker_key": "broker1",
                            "description": "Apple Inc"
                        },
                        "symbol": {
                            "id": "sym1",
                            "domain": "NASDAQ",
                            "exchange": "NASDAQ",
                            "symbol": "AAPL",
                            "currency": "USD"
                        },
                        "units": 10.0,
                        "unit_price": 150.0,
                        "currency": "USD",
                        "trade_date": "2022-01-01T00:00:00Z",
                        "settled_date": "2022-01-02T00:00:00Z",
                        "tx_type": "BUY"
                    }
                ]),
                expected_staging_txs_json: json!([
                    {
                        "id": 1,
                        "batch_dbid": 1,
                        "broker_key": "broker1",
                        "description": "Apple Inc",
                        "domain": "NASDAQ",
                        "exchange": "NASDAQ",
                        "symbol": "AAPL",
                        "symbol_currency": "USD",
                        "currency": "USD",
                        "account_id": "account1",
                        "units": 10.0,
                        "unit_price": 150.0,
                        "trade_date": "2022-01-01T00:00:00Z",
                        "settled_date": "2022-01-02T00:00:00Z",
                        "tx_type": "BUY"
                    }
                ]),
            },
        ];

        // Run each test case
        for test_case in test_cases.iter() {
            print!("test tests::test_stage_txs: {} ... ", test_case.name);

            // Deserialize input transactions from JSON
            let input_txs: Vec<Tx> = serde_json::from_value(test_case.input_txs_json.clone())
                .expect("Failed to deserialize input transactions");

            // Create a batch for this test case
            let mut exec = db.executor();
            let batch_dbid = db.create_batch(
                &mut exec,
                1, // user_dbid
                "TXS_TIMESERIES",
                chrono::DateTime::from_timestamp(1640995200, 0).unwrap(), // period_start
                chrono::DateTime::from_timestamp(1641081600, 0).unwrap() // period_end
            ).await.expect("Failed to create batch");

            // Stage the transactions
            let record_count = db.stage_txs(&mut exec, batch_dbid, Box::new(input_txs.clone().into_iter()))
                .await.expect("Failed to stage transactions");

            // Verify the record count
            assert_eq!(record_count, input_txs.len(), 
                "Record count mismatch for test case: {}", test_case.name);

            // Query the database to get the actual staged transactions
            let stmt = staging_txs::Entity::find()
                .filter(staging_txs::Column::BatchDbid.eq(batch_dbid));

            let actual_staging_txs = match &mut exec {
                Conn { db, .. } => {
                    stmt.all(db.as_ref())
                        .await.expect("Failed to query staging transactions")
                }
                TxExec { tx, .. } => {
                    stmt.all(tx)
                        .await.expect("Failed to query staging transactions")
                }
            };

            // Deserialize expected staging transactions from JSON
            let mut expected_staging_txs: Vec<staging_txs::Model> = serde_json::from_value(test_case.expected_staging_txs_json.clone())
                .expect("Failed to deserialize expected staging transactions");

            // Update the expected models with the actual batch_dbid and id values
            for (i, expected) in expected_staging_txs.iter_mut().enumerate() {
                expected.batch_dbid = batch_dbid;
                if let Some(actual) = actual_staging_txs.get(i) {
                    expected.id = actual.id;
                }
            }

            // Compare actual vs expected using PartialEq
            assert_eq!(actual_staging_txs.len(), expected_staging_txs.len(),
                "Number of staged transactions mismatch for test case: {}", test_case.name);

            for (actual, expected) in actual_staging_txs.iter().zip(expected_staging_txs.iter()) {
                assert_eq!(actual, expected, 
                    "Staging transaction mismatch for test case: {}", test_case.name);
            }

            println!("ok");
        }
    }
}
