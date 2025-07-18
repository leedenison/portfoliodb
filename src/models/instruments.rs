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
    #[sea_orm(has_many = "super::canonical_instr_descs::Entity")]
    CanonicalInstrDescs,
    #[sea_orm(has_many = "super::user_instr_descs::Entity")]
    UserInstrDescs,
    #[sea_orm(has_many = "super::canonical_instr_ids::Entity")]
    CanonicalInstrIds,
    #[sea_orm(has_many = "super::user_instr_ids::Entity")]
    UserInstrIds,
    #[sea_orm(has_many = "super::canonical_instr_symbols::Entity")]
    CanonicalInstrSymbols,
    #[sea_orm(has_many = "super::user_instr_symbols::Entity")]
    UserInstrSymbols,
    #[sea_orm(has_one = "super::derivatives::Entity")]
    Derivatives,
    #[sea_orm(has_many = "super::transactions::Entity")]
    Transactions,
    #[sea_orm(has_many = "super::prices::Entity")]
    Prices,
}

impl ActiveModelBehavior for ActiveModel {}
