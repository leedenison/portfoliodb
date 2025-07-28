pub mod portfolio_db {
    tonic::include_proto!("portfoliodb");
}

pub mod auth;
pub mod db;
pub mod ingest;
pub mod models;
pub mod rpc;

pub use portfolio_db::*;

#[cfg(test)]
mod tests {
    use crate::portfolio_db::{Tx, TxType};
    use serde_json;

    #[test]
    fn test_tx_deserialize_from_json() {
        // Create a sample JSON string representing a Tx
        let json_str = r#"{
            "account_id": "account_456",
            "description": {
                "id": "desc_789",
                "broker_key": "test_broker",
                "description": "AAPL Common Stock"
            },
            "symbol": {
                "id": "sym_001",
                "domain": "NASDAQ",
                "exchange": "NASDAQ",
                "symbol": "AAPL",
                "currency": "USD"
            },
            "units": 100.0,
            "unit_price": 150.25,
            "currency": "USD",
            "trade_date": "2022-01-01T00:00:00Z",
            "settled_date": "2022-01-02T00:00:00Z",
            "tx_type": "BUY"
        }"#;

        // Deserialize the JSON into a Tx struct
        let tx: Tx = serde_json::from_str(json_str).expect("Failed to deserialize Tx from JSON");

        // Verify the deserialized data matches the expected values
        assert_eq!(tx.id, "");
        assert_eq!(tx.account_id, "account_456");
        assert_eq!(tx.units, 100.0);
        assert_eq!(tx.unit_price, Some(150.25));
        assert_eq!(tx.currency, "USD");
        assert_eq!(tx.tx_type(), TxType::Buy);

        // Verify nested structs
        assert_eq!(tx.description.as_ref().unwrap().id, "desc_789");
        assert_eq!(tx.description.as_ref().unwrap().broker_key, "test_broker");
        assert_eq!(
            tx.description.as_ref().unwrap().description,
            "AAPL Common Stock"
        );

        assert_eq!(tx.symbol.as_ref().unwrap().id, "sym_001");
        assert_eq!(tx.symbol.as_ref().unwrap().domain, "NASDAQ");
        assert_eq!(tx.symbol.as_ref().unwrap().exchange, "NASDAQ");
        assert_eq!(tx.symbol.as_ref().unwrap().symbol, "AAPL");
        assert_eq!(tx.symbol.as_ref().unwrap().currency, "USD");

        // Verify timestamps (pbjson converts ISO strings to Timestamp)
        assert_eq!(tx.trade_date.as_ref().unwrap().seconds, 1640995200);
        assert_eq!(tx.settled_date.as_ref().unwrap().seconds, 1641081600);
    }
}
