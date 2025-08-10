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

#[derive(Copy, Clone, Debug, EnumIter)]
pub enum Relation {
    // This derivative's own instrument row (1:1)
    Instrument,
    // Underlying instrument this derivative refers to (1:N from Instrument's POV)
    Underlying,
}

impl sea_orm::RelationTrait for Relation {
    fn def(&self) -> sea_orm::RelationDef {
        match self {
            // derivative.instrument_dbid -> instrument.dbid
            Self::Instrument => Entity::belongs_to(super::instruments::Entity)
                .from(Column::InstrumentDbid)
                .to(super::instruments::Column::Dbid)
                .into(),

            // derivative.underlying_dbid -> instrument.dbid
            Self::Underlying => Entity::belongs_to(super::instruments::Entity)
                .from(Column::UnderlyingDbid)
                .to(super::instruments::Column::Dbid)
                .into(),
        }
    }
}

impl sea_orm::Related<super::instruments::Entity> for Entity {
    fn to() -> sea_orm::RelationDef {
        Relation::Instrument.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
