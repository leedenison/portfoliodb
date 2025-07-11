pub mod portfolio_db {
    tonic::include_proto!("portfoliodb");
}

pub mod rpc;
pub mod models;
pub mod database;

pub use portfolio_db::*; 