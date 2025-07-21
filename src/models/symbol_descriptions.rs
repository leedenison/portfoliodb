use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "symbol_descriptions")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub symbol_dbid: Option<i64>,
    pub user_dbid: i64,
    pub broker_key: String,
    pub description: String,
    pub canonical: bool,
    pub created_at: DateTime<Utc>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::symbols::Entity",
        from = "Column::SymbolDbid",
        to = "super::symbols::Column::Dbid"
    )]
    Symbol,
    #[sea_orm(
        belongs_to = "super::brokers::Entity",
        from = "Column::BrokerKey",
        to = "super::brokers::Column::Key"
    )]
    Broker,
    #[sea_orm(has_many = "super::transactions::Entity")]
    Transactions,
}

impl Related<super::symbols::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Symbol.def()
    }
}

impl Related<super::brokers::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Broker.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}

impl Entity {}
