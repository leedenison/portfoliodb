use crate::portfolio_db::{Price, Symbol};
use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;
use sea_orm::{NotSet, Set};
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, PartialEq, DeriveEntityModel, Serialize, Deserialize)]
#[sea_orm(table_name = "staging_prices")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub id: i64,
    pub batch_dbid: i64,
    pub domain: String,
    pub exchange: String,
    pub symbol: String,
    pub currency: String,
    pub price: f64,
    pub date_as_of: DateTime<Utc>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::batches::Entity",
        from = "Column::BatchDbid",
        to = "super::batches::Column::BatchDbid"
    )]
    Batch,
}

impl Related<super::batches::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Batch.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}

impl ActiveModel {
    pub fn with_batch_dbid(mut self, batch_dbid: i64) -> Self {
        self.batch_dbid = Set(batch_dbid);
        self
    }
}

impl From<Price> for ActiveModel {
    fn from(price: Price) -> Self {
        let Price {
            symbol,
            price: price_value,
            date_as_of,
        } = price;

        let (domain, exchange, sym, currency) = match symbol {
            Some(Symbol {
                id: _,
                domain,
                exchange,
                symbol: sym,
                currency,
            }) => (domain, exchange, sym, currency),
            None => (String::new(), String::new(), String::new(), String::new()),
        };

        let date_as_of = date_as_of
            .as_ref()
            .map(|ts| DateTime::from_timestamp(ts.seconds, ts.nanos as u32).unwrap_or_default())
            .unwrap_or_default();

        ActiveModel {
            id: NotSet,
            batch_dbid: Set(0),
            domain: Set(domain),
            exchange: Set(exchange),
            symbol: Set(sym),
            currency: Set(currency),
            price: Set(price_value),
            date_as_of: Set(date_as_of),
        }
    }
}
