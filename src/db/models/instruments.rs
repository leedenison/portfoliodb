use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "instruments")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub r#type: String, // Using raw identifier for 'type'
    pub created_at: DateTime<Utc>,
}

#[derive(Copy, Clone, Debug, EnumIter)]
pub enum Relation {
    InstrumentIds,
    Symbols,
    IsDerivative,
    Derivatives,
}

impl sea_orm::RelationTrait for Relation {
    fn def(&self) -> sea_orm::RelationDef {
        match self {
            // instrument.dbid -> instrument_identifiers.instrument_dbid
            Self::InstrumentIds => Entity::has_many(super::instrument_ids::Entity)
                .from(Column::Dbid)
                .to(super::instrument_ids::Column::InstrumentDbid)
                .into(),

            // instrument.dbid -> symbols.instrument_dbid
            Self::Symbols => Entity::has_many(super::symbols::Entity)
                .from(Column::Dbid)
                .to(super::symbols::Column::InstrumentDbid)
                .into(),

            // instrument.dbid -> derivative.instrument_dbid
            Self::IsDerivative => Entity::has_one(super::derivatives::Entity)
                .from(Column::Dbid)
                .to(super::derivatives::Column::InstrumentDbid)
                .into(),

            // instrument.dbid -> derivative.underlying_dbid
            Self::Derivatives => Entity::has_many(super::derivatives::Entity)
                .from(Column::Dbid)
                .to(super::derivatives::Column::UnderlyingDbid)
                .into(),
        }
    }
}

impl sea_orm::Related<super::derivatives::Entity> for Entity {
    fn to() -> sea_orm::RelationDef {
        Relation::IsDerivative.def()
    }
}

impl sea_orm::Related<super::instrument_ids::Entity> for Entity {
    fn to() -> sea_orm::RelationDef {
        Relation::InstrumentIds.def()
    }
}

impl sea_orm::Related<super::symbols::Entity> for Entity {
    fn to() -> sea_orm::RelationDef {
        Relation::Symbols.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
