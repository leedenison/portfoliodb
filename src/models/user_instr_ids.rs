use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "user_instr_ids")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub instrument_dbid: i64,
    pub user_dbid: i64,
    pub domain: String,
    pub id: String,
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
        belongs_to = "super::users::Entity",
        from = "Column::UserDbid",
        to = "super::users::Column::Dbid"
    )]
    User,
}

impl Related<super::instruments::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Instrument.def()
    }
}

impl Related<super::users::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::User.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
