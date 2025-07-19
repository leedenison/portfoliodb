use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "instruments")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub r#type: String, // Using raw identifier for 'type'
    pub created_at: DateTime<Utc>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(has_many = "super::instrument_descriptions::Entity")]
    InstrumentDescriptions,
    #[sea_orm(has_many = "super::instrument_ids::Entity")]
    InstrumentIds,
    #[sea_orm(has_many = "super::instrument_symbols::Entity")]
    InstrumentSymbols,
    #[sea_orm(has_one = "super::derivatives::Entity")]
    Derivatives,
    #[sea_orm(has_many = "super::transactions::Entity")]
    Transactions,
    #[sea_orm(has_many = "super::prices::Entity")]
    Prices,
}

impl ActiveModelBehavior for ActiveModel {}
