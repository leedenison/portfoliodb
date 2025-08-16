use crate::portfolio_db::{Derivative, Identifier, Instrument};
use crate::{prost_instrument_type, prost_option_style, prost_put_call};
use anyhow::Result;
use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;
use sea_orm::ConnectionTrait;
use sea_orm::QueryTrait;
use sea_orm::{NotSet, Set};
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, PartialEq, DeriveEntityModel, Serialize, Deserialize)]
#[sea_orm(table_name = "staging_instruments")]
pub struct Model {
    #[sea_orm(primary_key)]
    #[serde(default)]
    pub dbid: i64,
    pub batch_dbid: i64,
    pub type_: String,
    pub status: String,
    pub listing_mic: String,
    pub currency: String,
    // derivative fields
    pub underlying_namespace: String,
    pub underlying_domain: String,
    pub underlying_identifier: String,
    pub derivative_type: String,
    // option fields
    pub option_expiration_date: Option<DateTime<Utc>>,
    pub option_put_call: String,
    pub option_strike_price: Option<f64>,
    pub option_style: String,
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

impl Entity {}

impl ActiveModelBehavior for ActiveModel {}

impl ActiveModel {
    pub fn with_batch_dbid(mut self, batch_dbid: i64) -> Self {
        self.batch_dbid = Set(batch_dbid);
        self
    }
}

impl From<Instrument> for ActiveModel {
    fn from(instrument: Instrument) -> Self {
        let Instrument {
            r#type,
            listing_mic,
            currency,
            derivative,
            ..
        } = instrument;

        // Handle derivative and option fields
        let (
            underlying_namespace,
            underlying_domain,
            underlying_identifier,
            derivative_type,
            option_expiration_date,
            option_put_call,
            option_strike_price,
            option_style,
        ) = match derivative {
            Some(Derivative {
                underlying_id,
                option,
            }) => {
                let (underlying_namespace, underlying_domain, underlying_identifier) =
                    match underlying_id {
                        Some(Identifier {
                            namespace,
                            domain,
                            identifier,
                            ..
                        }) => (namespace, domain, identifier),
                        None => (String::new(), String::new(), String::new()),
                    };

                match option {
                    Some(opt) => {
                        let derivative_type = "OPTION".to_string();

                        let expiration_date = opt
                            .expiration_date
                            .as_ref()
                            .and_then(|ts| DateTime::from_timestamp(ts.seconds, ts.nanos as u32));

                        let put_call = prost_put_call::from_i32(opt.put_call() as i32);
                        let style = prost_option_style::from_i32(opt.style() as i32);

                        (
                            underlying_namespace,
                            underlying_domain,
                            underlying_identifier,
                            derivative_type,
                            expiration_date,
                            put_call,
                            Some(opt.strike_price),
                            style,
                        )
                    }
                    None => (
                        underlying_namespace,
                        underlying_domain,
                        underlying_identifier,
                        String::new(),
                        None,
                        String::new(),
                        None,
                        String::new(),
                    ),
                }
            }
            None => (
                String::new(),
                String::new(),
                String::new(),
                String::new(),
                None,
                String::new(),
                None,
                String::new(),
            ),
        };

        let instrument_type = prost_instrument_type::from_i32(r#type as i32);

        ActiveModel {
            dbid: NotSet,
            batch_dbid: Set(0),
            type_: Set(instrument_type),
            status: Set("ACTIVE".to_string()),
            listing_mic: Set(listing_mic),
            currency: Set(currency),
            underlying_namespace: Set(underlying_namespace),
            underlying_domain: Set(underlying_domain),
            underlying_identifier: Set(underlying_identifier),
            derivative_type: Set(derivative_type),
            option_expiration_date: Set(option_expiration_date),
            option_put_call: Set(option_put_call),
            option_strike_price: Set(option_strike_price),
            option_style: Set(option_style),
        }
    }
}
