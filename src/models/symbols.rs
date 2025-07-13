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
    pub description: String,
}

impl Model {
    /// Convert the symbol to a tuple of (domain, symbol, exchange, description)
    pub fn to_tuple(&self) -> (String, String, String, String) {
        (
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

impl From<((String, String, String, String), i64)> for ActiveModel {
    fn from(
        ((domain, symbol, exchange, description), instrument_id): (
            (String, String, String, String),
            i64,
        ),
    ) -> Self {
        Self {
            instrument_id: Set(instrument_id),
            domain: Set(domain),
            symbol: Set(symbol),
            exchange: Set(exchange),
            description: Set(description),
            ..Default::default()
        }
    }
}
