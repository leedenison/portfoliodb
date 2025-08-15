use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "instruments")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub r#type: String, // Using raw identifier for 'type'
    pub status: String,
    pub listing_mic: String,
    pub currency: String,
    pub created_at: DateTime<Utc>,
}

#[derive(Copy, Clone, Debug, EnumIter)]
pub enum Relation {
    Identifiers,
    IsDerivative,
    Derivatives,
    Transactions,
    Prices,
}

impl sea_orm::RelationTrait for Relation {
    fn def(&self) -> sea_orm::RelationDef {
        match self {
            // instrument.dbid -> identifiers.instrument_dbid
            Self::Identifiers => Entity::has_many(super::identifiers::Entity)
                .from(Column::Dbid)
                .to(super::identifiers::Column::InstrumentDbid)
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

            // instrument.dbid -> transactions.instrument_dbid
            Self::Transactions => Entity::has_many(super::transactions::Entity)
                .from(Column::Dbid)
                .to(super::transactions::Column::InstrumentDbid)
                .into(),

            // instrument.dbid -> prices.instrument_dbid
            Self::Prices => Entity::has_many(super::prices::Entity)
                .from(Column::Dbid)
                .to(super::prices::Column::InstrumentDbid)
                .into(),
        }
    }
}

impl sea_orm::Related<super::derivatives::Entity> for Entity {
    fn to() -> sea_orm::RelationDef {
        Relation::IsDerivative.def()
    }
}

impl sea_orm::Related<super::identifiers::Entity> for Entity {
    fn to() -> sea_orm::RelationDef {
        Relation::Identifiers.def()
    }
}

impl sea_orm::Related<super::transactions::Entity> for Entity {
    fn to() -> sea_orm::RelationDef {
        Relation::Transactions.def()
    }
}

impl sea_orm::Related<super::prices::Entity> for Entity {
    fn to() -> sea_orm::RelationDef {
        Relation::Prices.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
