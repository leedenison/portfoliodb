use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "brokers")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub key: String,
    pub name: String,
    pub created_at: DateTime<Utc>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(has_many = "super::instrument_descriptions::Entity")]
    InstrumentDescriptions,
}

impl ActiveModelBehavior for ActiveModel {}
