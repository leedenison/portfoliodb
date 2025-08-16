use crate::portfolio_db::Identifier;
use sea_orm::entity::prelude::*;
use sea_orm::{NotSet, Set};
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, PartialEq, DeriveEntityModel, Serialize, Deserialize)]
#[sea_orm(table_name = "staging_identifiers")]
pub struct Model {
    #[sea_orm(primary_key)]
    #[serde(default)]
    pub dbid: i64,
    pub batch_dbid: i64,
    pub instrument_dbid: i64,
    pub namespace: String,
    pub domain: String,
    pub identifier: String,
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

    pub fn with_instrument_dbid(mut self, instrument_dbid: i64) -> Self {
        self.instrument_dbid = Set(instrument_dbid);
        self
    }
}

impl From<Identifier> for ActiveModel {
    fn from(identifier: Identifier) -> Self {
        let Identifier {
            namespace,
            domain,
            identifier: id,
            ..
        } = identifier;

        ActiveModel {
            dbid: NotSet,
            batch_dbid: Set(0),
            instrument_dbid: Set(0),
            namespace: Set(namespace),
            domain: Set(domain),
            identifier: Set(id),
        }
    }
}
