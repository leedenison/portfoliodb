use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "prices")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub symbol_dbid: i64,
    pub price: f64,
    pub date_as_of: DateTime<Utc>,
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
}

impl Related<super::symbols::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Symbol.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
