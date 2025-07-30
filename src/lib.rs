pub mod portfolio_db {
    tonic::include_proto!("portfoliodb");
}

pub mod auth;
pub mod db;
pub mod ingest;
pub mod models;
pub mod proto;
pub mod rpc;

pub use portfolio_db::*;

prost_enum!(prost_tx_type, crate::portfolio_db::TxType);
prost_enum!(prost_instrument_type, crate::portfolio_db::InstrumentType);
prost_enum!(prost_derivative_type, crate::portfolio_db::DerivativeType);
prost_enum!(prost_put_call, crate::portfolio_db::PutCall);
prost_enum!(prost_error_code, crate::portfolio_db::ErrorCode);
