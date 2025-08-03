use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "transactions")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub user_dbid: i64,
    pub symbol_dbid: Option<i64>,
    pub symbol_description_dbid: Option<i64>,
    pub account_id: String,
    pub units: f64,
    pub unit_price: Option<f64>,
    pub currency: String, // ISO 4217 currency code (e.g., USD, EUR, GBP)
    pub trade_date: DateTime<Utc>,
    pub settled_date: Option<DateTime<Utc>>,
    pub tx_type: String,
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
        belongs_to = "super::symbol_descriptions::Entity",
        from = "Column::SymbolDescriptionDbid",
        to = "super::symbol_descriptions::Column::Dbid"
    )]
    SymbolDescription,
}

impl Related<super::symbols::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Symbol.def()
    }
}

impl Related<super::symbol_descriptions::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::SymbolDescription.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
