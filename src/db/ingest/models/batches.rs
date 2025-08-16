use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "staging_batches")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub user_dbid: i64,
    pub batch_type: String,
    pub status: String,
    pub broker_key: String,
    pub period_start: DateTime<Utc>,
    pub period_end: DateTime<Utc>,
    pub total_records: i32,
    pub processed_records: i32,
    pub error_count: i32,
    pub created_at: DateTime<Utc>,
    pub processed_at: Option<DateTime<Utc>>,
    pub error_message: Option<String>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(has_many = "super::staging_txs::Entity")]
    StagingTxs,
    #[sea_orm(has_many = "super::staging_instruments::Entity")]
    StagingInstruments,
    #[sea_orm(has_many = "super::staging_identifiers::Entity")]
    StagingIdentifiers,
}

impl ActiveModelBehavior for ActiveModel {}
