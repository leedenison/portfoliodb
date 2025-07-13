use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "derivatives")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub id: i64,
    pub instrument_id: i64,
    pub underlying_instrument_id: i64,
    pub expiration_date: DateTime<Utc>,
    pub put_call: String,
    pub strike_price: f64,
    pub multiplier: f64,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::instruments::Entity",
        from = "Column::InstrumentId",
        to = "super::instruments::Column::Id"
    )]
    Instrument,
    #[sea_orm(
        belongs_to = "super::instruments::Entity",
        from = "Column::UnderlyingInstrumentId",
        to = "super::instruments::Column::Id"
    )]
    Underlying,
}

impl Related<super::instruments::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Instrument.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
