use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "derivatives")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub instrument_dbid: i64,
    pub underlying_dbid: i64,
    pub expiration_date: DateTime<Utc>,
    pub put_call: String,
    pub strike_price: f64,
    pub multiplier: f64,
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
    #[sea_orm(
        belongs_to = "super::instruments::Entity",
        from = "Column::UnderlyingDbid",
        to = "super::instruments::Column::Dbid"
    )]
    Underlying,
}

impl Related<super::instruments::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Instrument.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
