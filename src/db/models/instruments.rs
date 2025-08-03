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

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(has_many = "super::instrument_ids::Entity")]
    InstrumentIds,
    #[sea_orm(has_many = "super::symbols::Entity")]
    Symbols,
    #[sea_orm(has_one = "super::derivatives::Entity")]
    Derivatives,
}

impl ActiveModelBehavior for ActiveModel {}

impl Related<super::instrument_ids::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::InstrumentIds.def()
    }
}

impl Related<super::symbols::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Symbols.def()
    }
}

impl Related<super::derivatives::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Derivatives.def()
    }
}
