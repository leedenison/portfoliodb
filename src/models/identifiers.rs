use sea_orm::ActiveValue::Set;
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "identifiers")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub instrument_dbid: i64,
    pub id: String,
    pub domain: String,
    pub symbol: String,
    pub exchange: String,
    pub description: String,
}

impl Model {
    /// Convert the identifier to a tuple of (id, domain, symbol, exchange, description)
    pub fn to_tuple(&self) -> (String, String, String, String, String) {
        (
            self.id.clone(),
            self.domain.clone(),
            self.symbol.clone(),
            self.exchange.clone(),
            self.description.clone(),
        )
    }
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::instruments::Entity",
        from = "Column::InstrumentDbid",
        to = "super::instruments::Column::Dbid"
    )]
    Instrument,
}

impl Related<super::instruments::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Instrument.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}

impl From<((String, String, String, String, String), i64)> for ActiveModel {
    fn from(
        ((id, domain, symbol, exchange, description), instrument_dbid): (
            (String, String, String, String, String),
            i64,
        ),
    ) -> Self {
        Self {
            instrument_dbid: Set(instrument_dbid),
            id: Set(id),
            domain: Set(domain),
            symbol: Set(symbol),
            exchange: Set(exchange),
            description: Set(description),
            ..Default::default()
        }
    }
}
