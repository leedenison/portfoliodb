use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "symbols")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub id: i64,
    pub instrument_id: i64,
    pub domain: String,
    pub symbol: String,
    pub exchange: String,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::instruments::Entity",
        from = "Column::InstrumentId",
        to = "super::instruments::Column::Id"
    )]
    Instrument,
}

impl Related<super::instruments::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Instrument.def()
    }
}

impl ActiveModelBehavior for ActiveModel {} 