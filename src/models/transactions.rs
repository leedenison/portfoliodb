use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "transactions")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub account_id: String,
    pub instrument_dbid: i64,
    pub units: f64,
    pub unit_price: Option<f64>,
    pub currency: String, // ISO 4217 currency code (e.g., USD, EUR, GBP)
    pub trade_date: DateTime<Utc>,
    pub settled_date: Option<DateTime<Utc>>,
    pub tx_type: String,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::instruments::Entity",
        from = "Column::InstrumentDbid",
        to = "super::instruments::Column::Dbid"
    )]
    Instrument,
}

impl Related<super::instruments::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Instrument.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
