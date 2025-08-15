use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "derivatives")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub instrument_dbid: i64,
    pub underlying_dbid: i64,
    pub r#type: String, // Using raw identifier for 'type'
    pub created_at: DateTime<Utc>,
}

#[derive(Copy, Clone, Debug, EnumIter)]
pub enum Relation {
    // This derivative's own instrument row (1:1)
    Instrument,
    // Underlying instrument this derivative refers to (1:N from Instrument's POV)
    Underlying,
    // One-to-one relationship with option_derivatives
    OptionDerivative,
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

            // derivative.dbid -> option_derivatives.derivative_dbid (1:1)
            Self::OptionDerivative => Entity::has_one(super::option_derivatives::Entity)
                .from(Column::Dbid)
                .to(super::option_derivatives::Column::DerivativeDbid)
                .into(),
        }
    }
}

impl sea_orm::Related<super::instruments::Entity> for Entity {
    fn to() -> sea_orm::RelationDef {
        Relation::Instrument.def()
    }
}

impl sea_orm::Related<super::option_derivatives::Entity> for Entity {
    fn to() -> sea_orm::RelationDef {
        Relation::OptionDerivative.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
