use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "instrument_descriptions")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub instrument_dbid: i64,
    pub user_dbid: Option<i64>,
    pub broker_dbid: i64,
    pub description: String,
    pub canonical: bool,
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
        belongs_to = "super::brokers::Entity",
        from = "Column::BrokerDbid",
        to = "super::brokers::Column::Dbid"
    )]
    Broker,
}

impl Related<super::instruments::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Instrument.def()
    }
}

impl Related<super::brokers::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Broker.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
