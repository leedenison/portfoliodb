pub mod portfolio_db {
    tonic::include_proto!("portfoliodb");
}

pub mod auth;
pub mod db;
pub mod models;
pub mod rpc;

pub use portfolio_db::*;
