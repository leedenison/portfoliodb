use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "symbols")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub instrument_dbid: i64,
    pub domain: String,
    pub exchange: String,
    pub symbol: String,
    pub currency: String,
    pub created_at: DateTime<Utc>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::instruments::Entity",
        from = "Column::InstrumentDbid",
        to = "super::instruments::Column::Dbid"
    )]
    Instrument,
    #[sea_orm(has_many = "super::symbol_descriptions::Entity")]
    SymbolDescriptions,
    #[sea_orm(has_many = "super::transactions::Entity")]
    Transactions,
}

impl Related<super::instruments::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Instrument.def()
    }
}

impl Related<super::symbol_descriptions::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::SymbolDescriptions.def()
    }
}

impl Related<super::transactions::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Transactions.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}

impl Entity {}
