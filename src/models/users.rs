use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "users")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub email: String,
    pub username: String,
    pub created_at: DateTime<Utc>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(has_many = "super::user_instr_descs::Entity")]
    UserInstrDescs,
    #[sea_orm(has_many = "super::user_instr_ids::Entity")]
    UserInstrIds,
    #[sea_orm(has_many = "super::user_instr_symbols::Entity")]
    UserInstrSymbols,
    #[sea_orm(has_many = "super::transactions::Entity")]
    Transactions,
}

impl ActiveModelBehavior for ActiveModel {}
