use crate::portfolio_db::{Symbol, SymbolDescription, Tx};
use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;
use sea_orm::{NotSet, Set};
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, PartialEq, DeriveEntityModel, Serialize, Deserialize)]
#[sea_orm(table_name = "staging_txs")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub id: i64,
    pub batch_dbid: i64,
    pub broker_key: String,
    pub description: String,
    pub domain: String,
    pub exchange: String,
    pub symbol: String,
    pub symbol_currency: String,
    pub currency: String,
    pub account_id: String,
    pub units: f64,
    pub unit_price: Option<f64>,
    pub trade_date: DateTime<Utc>,
    pub settled_date: Option<DateTime<Utc>>,
    pub tx_type: String,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::batches::Entity",
        from = "Column::BatchDbid",
        to = "super::batches::Column::BatchDbid"
    )]
    Batches,
}

impl Related<super::batches::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Batches.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}

impl ActiveModel {
    pub fn with_batch_dbid(mut self, batch_dbid: i64) -> Self {
        self.batch_dbid = Set(batch_dbid);
        self
    }
}

impl From<Tx> for ActiveModel {
    fn from(tx: Tx) -> Self {
        let Tx {
            description,
            symbol,
            account_id,
            units,
            unit_price,
            currency: tx_currency,
            trade_date,
            settled_date,
            tx_type,
            ..
        } = tx;

        let (broker_key, desc) = match description {
            Some(SymbolDescription {
                id: _,
                broker_key,
                description: desc,
            }) => (broker_key, desc),
            None => (String::new(), String::new()),
        };

        let (domain, exchange, sym, sym_currency) = match symbol {
            Some(Symbol {
                id: _,
                domain,
                exchange,
                symbol: sym,
                currency,
            }) => (domain, exchange, sym, currency),
            None => (String::new(), String::new(), String::new(), String::new()),
        };

        let trade_date = trade_date
            .as_ref()
            .map(|ts| DateTime::from_timestamp(ts.seconds, ts.nanos as u32).unwrap_or_default())
            .unwrap_or_default();

        let settled_date = settled_date
            .as_ref()
            .map(|ts| DateTime::from_timestamp(ts.seconds, ts.nanos as u32).unwrap_or_default());

        let tx_type = crate::prost_tx_type::from_i32(tx_type);

        ActiveModel {
            id: NotSet,
            batch_dbid: Set(0),
            broker_key: Set(broker_key),
            description: Set(desc),
            domain: Set(domain),
            exchange: Set(exchange),
            symbol: Set(sym),
            symbol_currency: Set(sym_currency),
            currency: Set(tx_currency),
            account_id: Set(account_id),
            units: Set(units),
            unit_price: Set(unit_price),
            trade_date: Set(trade_date),
            settled_date: Set(settled_date),
            tx_type: Set(tx_type),
        }
    }
}
