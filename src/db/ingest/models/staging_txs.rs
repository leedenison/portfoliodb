use crate::portfolio_db::{Symbol, SymbolDescription, Tx};
use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;
use sea_orm::{NotSet, Set, Select, Condition};
use serde::{Deserialize, Serialize};
use anyhow::Result;
use crate::db::models;

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

#[derive(Copy, Clone, Debug, EnumIter)]
pub enum Relation {
    Batch,
    Symbol,
}

impl RelationTrait for Relation {
    fn def(&self) -> RelationDef {
        match self {
            Self::Batch => Entity::belongs_to(super::batches::Entity)
                .from(Column::BatchDbid)
                .to(super::batches::Column::BatchDbid)
                .into(),
            Self::Symbol => Entity::belongs_to(models::Symbols)
                .from(Column::Domain)
                .to(models::symbols::Column::Domain)
                .from(Column::Exchange)
                .to(models::symbols::Column::Exchange)
                .from(Column::Symbol)
                .to(models::symbols::Column::Symbol)
                .into(),
        }
    }
}

impl Related<super::batches::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Batch.def()
    }
}

impl Related<models::Symbols> for Entity {
    fn to() -> RelationDef {
        Relation::Symbol.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}

impl Entity {
    /// Returns the query for finding invalid staged transactions.
    /// 
    /// Invalid transactions are those with empty description and incomplete symbol.
    /// 
    /// # Arguments
    /// * `batch_dbid` - The batch database ID to filter by
    /// 
    /// # Returns
    /// * `Select<Entity>` - The query builder for invalid transactions
    pub fn find_invalid_txs(batch_dbid: i64) -> Select<Entity> {
        Entity::find()
            .filter(Column::BatchDbid.eq(batch_dbid))
            .filter(
                Condition::all()
                    .add(Condition::any()
                        .add(Column::BrokerKey.eq(""))
                        .add(Column::Description.eq("")))
                    .add(Condition::any()
                        .add(Column::Domain.eq(""))
                        .add(Column::Exchange.eq(""))
                        .add(Column::Symbol.eq("")))
            )
    }

    /// Returns all invalid staged transactions for a given batch
    /// 
    /// # Arguments
    /// * `exec` - Database executor
    /// * `batch_dbid` - The batch database ID to filter by
    /// 
    /// # Returns
    /// * `Ok(Vec<Model>)` - Vector of invalid transaction models
    /// * `Err` if a database error occurs
    pub async fn all_invalid_txs<E>(
        exec: &E,
        batch_dbid: i64,
    ) -> Result<Vec<Model>>
    where
        E: sea_orm::ConnectionTrait,
    {
        Self::find_invalid_txs(batch_dbid).all(exec).await
            .map_err(|e| anyhow::anyhow!("Failed to fetch invalid transactions: {}", e))
    }

    /// Returns count of invalid staged transactions for a given batch
    /// 
    /// # Arguments
    /// * `exec` - Database executor
    /// * `batch_dbid` - The batch database ID to filter by
    /// 
    /// # Returns
    /// * `Ok(u64)` - Count of invalid transactions
    /// * `Err` if a database error occurs
    pub async fn count_invalid_txs<E>(
        exec: &E,
        batch_dbid: i64,
    ) -> Result<u64>
    where
        E: sea_orm::ConnectionTrait,
    {
        Self::find_invalid_txs(batch_dbid).count(exec).await
            .map_err(|e| anyhow::anyhow!("Failed to count invalid transactions: {}", e))
    }

    /// Returns all complete symbols from staged transactions with their corresponding existing symbols
    /// 
    /// This performs a left join to find staged transactions with complete symbol information
    /// (non-empty domain, exchange, and symbol) and their corresponding symbols in the database.
    /// 
    /// # Arguments
    /// * `exec` - Database executor
    /// * `batch_dbid` - The batch database ID to filter by
    /// 
    /// # Returns
    /// * `Ok(Vec<(Model, Option<Symbol>)>)` - Vector of staged transactions with optional existing symbols
    /// * `Err` if a database error occurs
    pub async fn all_complete_symbols_with_existing<E>(
        exec: &E,
        batch_dbid: i64,
    ) -> Result<Vec<(Model, Option<models::Symbol>)>>
    where
        E: sea_orm::ConnectionTrait,
    {
        Entity::find()
            .filter(Column::BatchDbid.eq(batch_dbid))
            .filter(Column::Domain.ne(""))
            .filter(Column::Exchange.ne(""))
            .filter(Column::Symbol.ne(""))
            .find_also_related(models::Symbols)
            .all(exec).await
            .map_err(|e| anyhow::anyhow!("Failed to fetch complete symbols with existing: {}", e))
    }
}

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