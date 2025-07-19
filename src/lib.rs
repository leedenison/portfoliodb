pub mod portfolio_db {
    tonic::include_proto!("portfoliodb");
}

pub mod auth;
pub mod database;
pub mod models;
pub mod rpc;
pub mod transaction;

pub use portfolio_db::*;
