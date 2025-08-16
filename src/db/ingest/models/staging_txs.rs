use crate::portfolio_db::Tx;
use crate::prost_tx_type;
use anyhow::Result;
use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;
use sea_orm::{NotSet, Set};
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, PartialEq, DeriveEntityModel, Serialize, Deserialize)]
#[sea_orm(table_name = "staging_txs")]
pub struct Model {
    #[sea_orm(primary_key)]
    #[serde(default)]
    pub dbid: i64,
    pub batch_dbid: i64,
    pub instrument_namespace: String,
    pub instrument_domain: String,
    pub instrument_identifier: String,
    pub account_id: String,
    pub currency: String,
    pub units: f64,
    pub unit_price: Option<f64>,
    pub trade_date: DateTime<Utc>,
    pub settled_date: Option<DateTime<Utc>>,
    pub tx_type: String,
}

#[derive(Copy, Clone, Debug, EnumIter)]
pub enum Relation {
    Batch,
}

impl RelationTrait for Relation {
    fn def(&self) -> RelationDef {
        match self {
            Self::Batch => Entity::belongs_to(super::batches::Entity)
                .from(Column::BatchDbid)
                .to(super::batches::Column::Dbid)
                .into(),
        }
    }
}

impl Related<super::batches::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Batch.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}

impl Entity {}

impl ActiveModel {
    pub fn with_batch_dbid(mut self, batch_dbid: i64) -> Self {
        self.batch_dbid = Set(batch_dbid);
        self
    }
}

impl From<Tx> for ActiveModel {
    fn from(tx: Tx) -> Self {
        let Tx {
            identifier,
            account_id,
            units,
            unit_price,
            currency,
            trade_date,
            settled_date,
            tx_type,
            ..
        } = tx;

        let (namespace, domain, id) = match identifier {
            Some(id) => (id.namespace, id.domain, id.identifier),
            None => (String::new(), String::new(), String::new()),
        };

        let trade_date = trade_date
            .as_ref()
            .map(|ts| DateTime::from_timestamp(ts.seconds, ts.nanos as u32).unwrap_or_default())
            .unwrap_or_default();

        let settled_date = settled_date
            .as_ref()
            .map(|ts| DateTime::from_timestamp(ts.seconds, ts.nanos as u32).unwrap_or_default());

        let tx_type = prost_tx_type::from_i32(tx_type as i32);

        ActiveModel {
            dbid: NotSet,
            batch_dbid: Set(0),
            instrument_namespace: Set(namespace),
            instrument_domain: Set(domain),
            instrument_identifier: Set(id),
            account_id: Set(account_id),
            currency: Set(currency),
            units: Set(units),
            unit_price: Set(unit_price),
            trade_date: Set(trade_date),
            settled_date: Set(settled_date),
            tx_type: Set(tx_type),
        }
    }
}
